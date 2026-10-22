package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Jaydee94/remedy/internal/loginrelay"
	"github.com/Jaydee94/remedy/internal/provider"
)

// The login relay drives `claude auth login` for the Setup page (docs/specs/2026-10-21-ui-login-relay-design.md). It
// never uses Execute and never posts what the CLI prints: stdout is read as bytes until the sign-in URL, everything
// after it on both streams is dropped, and only the phrase of a rejected code is looked for, as a boolean. The URL and
// the code live in loginrelay.Material holders; logs carry fixed words and numbers only.

// LoginTimings are the waits of the relay. A zero field takes its default.
type LoginTimings struct {
	URL          time.Duration // starting without a URL: no_url (default 30 s)
	Submit       time.Duration // a code written, no result: timeout (default 60 s)
	ConfirmEvery time.Duration // the login check while submitting, when the login was missing before (default 2 s)
	ExitGrace    time.Duration // the child's time to exit by itself after the check said ok (default 10 s)
	Relogin      time.Duration // a re-login whose child is still alive after this: timeout (default 60 s)
	Backoff      time.Duration // the wait after a 404, the control plane has no relay (default 60 s)
}

func (t LoginTimings) withDefaults() LoginTimings {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&t.URL, 30*time.Second)
	def(&t.Submit, 60*time.Second)
	def(&t.ConfirmEvery, 2*time.Second)
	def(&t.ExitGrace, 10*time.Second)
	def(&t.Relogin, 60*time.Second)
	def(&t.Backoff, 60*time.Second)
	return t
}

// loginRetry is the longest wait after a failed poll: the control plane fails a session after 60 s without a poll.
const loginRetry = 3 * time.Second

// maxLoginHead bounds what is read of stdout before the URL line.
const maxLoginHead = 4 << 10

var (
	// openingLine is the CLI's first line: "Opening browser to sign in" and U+2026.
	openingLine = []byte("Opening browser to sign in" + string(rune(0x2026)))
	visitPrefix = []byte("If the browser didn't open, visit: ")
	// invalidPhrase is the CLI's sentence for a rejected code, matched on both streams.
	invalidPhrase = []byte("Invalid code")
)

// LoginPause tells the login checker that a login is rewriting the CLI's config. The zero value is not paused, and a
// nil *LoginPause is never paused.
type LoginPause struct{ on atomic.Bool }

func (p *LoginPause) Set(on bool) {
	if p != nil {
		p.on.Store(on)
	}
}

func (p *LoginPause) Active() bool { return p != nil && p.on.Load() }

// NewBoot is a random id for one runner process: the control plane fails a session whose runner restarted.
func NewBoot() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails (crypto/rand)
	return hex.EncodeToString(b[:])
}

// LoginRelay polls the control plane for login commands and drives the CLI's login. The poll loop is the one goroutine
// that executes commands; each child has a watcher goroutine that posts its reports. Run it only when the runner
// is in subscription mode (the relay has no switch).
type LoginRelay struct {
	Client  *Client
	Claude  provider.Claude
	Check   func(context.Context) (provider.LoginState, string) // the CLI's own login check; default Claude.LoginCheck
	Status  *Status                                             // says whether the login was ok before an attempt
	Env     []string                                            // the parent environment, filtered by LoginCommand
	Busy    func() bool                                         // optional: a run is running
	Pause   *LoginPause                                         // set while a child runs
	Kick    func()                                              // optional: runs the login check now
	Prune   func()                                              // optional: cleans up after a session
	Timings LoginTimings
	Log     *slog.Logger

	onCodeWritten  func(loginrelay.Material) // tests only: the code holder after the write
	beforeCodeWait func(*loginChild)         // tests only: runs in the watcher before it waits for the code

	timings LoginTimings
	boot    string
	wg      sync.WaitGroup // the watchers

	mu   sync.Mutex
	sess relaySession
}

// relaySession is the one session the runner holds. It has no Report or PollAnswer in it, only plain fields.
type relaySession struct {
	id      string
	ack     int64 // the last command number executed
	attempt int   // the starts executed
	child   *loginChild
	active  bool // the checker is paused for this session
}

// Run polls until ctx ends, then stops the child and waits for its watcher.
func (l *LoginRelay) Run(ctx context.Context) {
	l.timings = l.Timings.withDefaults()
	if l.Log == nil {
		l.Log = slog.New(slog.DiscardHandler)
	}
	l.boot = NewBoot()
	defer l.shutdown()
	for ctx.Err() == nil {
		ans, err := l.Client.LoginPoll(ctx, l.pollRequest())
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, ErrLoginAbsent):
			l.Log.Warn("the control plane offers no login relay")
			sleep(ctx, l.timings.Backoff)
		case err != nil:
			l.Log.Warn("login poll failed")
			sleep(ctx, min(l.timings.Backoff, loginRetry))
		case ans != nil:
			l.execute(ctx, ans)
		}
	}
}

func (l *LoginRelay) pollRequest() loginrelay.PollRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return loginrelay.PollRequest{Boot: l.boot, Session: l.sess.id, Ack: l.sess.ack}
}

// execute runs one command. The code leaves the answer for a Material at once and is zeroed when this returns.
func (l *LoginRelay) execute(ctx context.Context, ans *loginrelay.PollAnswer) {
	code := loginrelay.NewMaterial(ans.Code)
	ans.Code = ""
	defer code.Zero()
	switch loginrelay.Action(ans.Action) {
	case loginrelay.ActionStart:
		l.start(ctx, ans.Session, ans.Seq)
	case loginrelay.ActionCode:
		l.writeCode(ans.Session, ans.Seq, code)
	case loginrelay.ActionCancel:
		l.cancel(ans.Session)
	default:
		l.Log.Warn("unknown login command")
	}
}

// start runs a new child for the session: attempt 1, or the next attempt after a rejected code.
func (l *LoginRelay) start(ctx context.Context, id string, seq int64) {
	if id == "" || seq <= 0 {
		return
	}
	l.mu.Lock()
	if l.sess.id == id && seq <= l.sess.ack {
		l.mu.Unlock()
		return // executed already: a lost ack
	}
	prev, other := l.sess.child, l.sess.id != "" && l.sess.id != id
	l.mu.Unlock()
	if prev != nil {
		// The old child is gone before the new one starts.
		l.stopChild(prev)
	}
	if other {
		l.release(l.sessionID())
	}

	l.mu.Lock()
	if l.sess.id != id {
		l.sess = relaySession{id: id}
	}
	l.sess.ack = seq
	l.sess.attempt++
	attempt := min(l.sess.attempt, 3)
	l.mu.Unlock()

	if l.Busy != nil && l.Busy() {
		// Belt and braces: the control plane does not start a login while a run is running.
		l.report(ctx, id, attempt, loginrelay.StateFailed, loginrelay.ReasonRunActive, loginrelay.Material{})
		return
	}
	loggedIn := l.Status.Report().Login == string(provider.LoginOK)

	l.mu.Lock()
	l.sess.active = true
	l.mu.Unlock()
	l.Pause.Set(true)
	ch, err := l.spawn(ctx, id, attempt, loggedIn)
	if err != nil {
		l.Log.Warn("the login could not start")
		l.report(ctx, id, attempt, loginrelay.StateFailed, loginrelay.ReasonProcessExited, loginrelay.Material{})
		l.release(id)
		return
	}
	l.mu.Lock()
	l.sess.child = ch
	l.mu.Unlock()
	l.wg.Add(1)
	go l.watch(ctx, ch)
}

// writeCode writes the code to the child of the session, at most once per command number and at most once per child.
// A code for a session the runner does not hold is dropped unwritten.
func (l *LoginRelay) writeCode(id string, seq int64, code loginrelay.Material) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id == "" || id != l.sess.id || seq <= l.sess.ack {
		return
	}
	l.sess.ack = seq
	ch := l.sess.child
	if ch == nil || !ch.awaiting || ch.written {
		return
	}
	if !codeShape(code.Bytes()) {
		l.Log.Warn("login code refused: not one line of printable characters")
		return
	}
	ch.written = true
	buf := make([]byte, code.Len()+1)
	copy(buf, code.Bytes())
	buf[len(buf)-1] = '\n'
	_, err := ch.stdin.Write(buf)
	clear(buf)
	code.Zero()
	if l.onCodeWritten != nil {
		l.onCodeWritten(code)
	}
	if err != nil {
		l.Log.Warn("the code could not be written to the login")
	}
	close(ch.code)
}

// codeShape is ValidateCode on bytes, without a string copy: 8 to 512 bytes of 0x21 to 0x7E.
func codeShape(b []byte) bool {
	if len(b) < loginrelay.MinCodeBytes || len(b) > loginrelay.MaxCodeBytes {
		return false
	}
	for _, c := range b {
		if c < 0x21 || c > 0x7E {
			return false
		}
	}
	return true
}

// cancel stops the session's child and forgets the session, so the next poll no longer names it. It runs for every
// cancel, numbered or seq 0.
func (l *LoginRelay) cancel(id string) {
	l.mu.Lock()
	if id == "" || id != l.sess.id {
		l.mu.Unlock()
		return
	}
	ch := l.sess.child
	l.mu.Unlock()
	if ch != nil {
		l.stopChild(ch)
	}
	l.release(id)
	l.mu.Lock()
	if l.sess.id == id {
		l.sess = relaySession{}
	}
	l.mu.Unlock()
}

func (l *LoginRelay) sessionID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sess.id
}

// release ends the pause of the session once: the checker is unpaused and kicked, and the cleanup runs.
func (l *LoginRelay) release(id string) {
	l.mu.Lock()
	if l.sess.id != id || !l.sess.active {
		l.mu.Unlock()
		return
	}
	l.sess.active = false
	l.mu.Unlock()
	l.Pause.Set(false)
	if l.Kick != nil {
		l.Kick()
	}
	if l.Prune != nil {
		l.Prune()
	}
}

// stopChild ends a child the poll loop no longer wants: its watcher reports nothing.
func (l *LoginRelay) stopChild(ch *loginChild) {
	ch.stopped.Store(true)
	ch.stop()
	<-ch.done
}

func (l *LoginRelay) shutdown() {
	l.mu.Lock()
	ch := l.sess.child
	l.mu.Unlock()
	if ch != nil {
		l.stopChild(ch)
	}
	l.wg.Wait()
	l.Pause.Set(false)
}

// report posts a state report. It logs fixed fields only; the URL goes on the wire and nowhere else.
func (l *LoginRelay) report(ctx context.Context, id string, attempt int, state loginrelay.State, reason loginrelay.Reason, url loginrelay.Material) {
	rep := loginrelay.Report{Session: id, State: string(state), Attempt: attempt, Reason: reason}
	if url.Len() > 0 {
		rep.URL = string(url.Bytes())
	}
	l.Log.Info("login session", "state", string(state), "attempt", attempt, "reason", string(reason))
	if err := l.Client.LoginReport(ctx, rep); err != nil && ctx.Err() == nil {
		l.Log.Warn("login report failed", "state", string(state))
	}
}

func (l *LoginRelay) check(ctx context.Context) provider.LoginState {
	cctx, cancel := context.WithTimeout(ctx, loginCheckTimeout)
	defer cancel()
	if l.Check != nil {
		state, _ := l.Check(cctx)
		return state
	}
	state, _ := l.Claude.LoginCheck(cctx, l.Env)
	return state
}

// loginChild is one `claude auth login` process.
type loginChild struct {
	session  string
	attempt  int
	loggedIn bool // the login was ok before this attempt: only the exit code counts
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	stdin    io.WriteCloser
	out      *cliOutput

	exited   chan struct{} // closed when Wait returned
	done     chan struct{} // closed when the watcher is finished
	code     chan struct{} // closed when the code was written
	stopped  atomic.Bool   // the poll loop stopped it: no report
	stopOnce sync.Once

	// guarded by LoginRelay.mu
	awaiting bool // the URL was found: a code may be written
	written  bool
}

// spawn starts the child in a process group of its own, with stdout and stderr going to the relay's reader.
func (l *LoginRelay) spawn(ctx context.Context, id string, attempt int, loggedIn bool) (*loginChild, error) {
	cctx, cancel := context.WithCancel(ctx)
	cmd := l.Claude.LoginCommand(cctx, l.Env)
	out := newCLIOutput()
	cmd.Stdout = streamWriter{out: out, stream: 0}
	cmd.Stderr = streamWriter{out: out, stream: 1}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	ch := &loginChild{
		session: id, attempt: attempt, loggedIn: loggedIn, cmd: cmd, cancel: cancel, stdin: stdin, out: out,
		exited: make(chan struct{}), done: make(chan struct{}), code: make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait() // the outcome is in ProcessState; the error may name a path
		close(ch.exited)
	}()
	return ch, nil
}

// stop ends the child: SIGTERM to the group (cmd.Cancel), Go's kill after WaitDelay, and after Wait a best-effort
// SIGKILL of the group for whatever it left behind.
func (ch *loginChild) stop() {
	ch.stopOnce.Do(func() {
		ch.cancel()
		<-ch.exited
		_ = provider.SignalGroup(ch.cmd, syscall.SIGKILL)
	})
}

// succeeded says whether the child exited 0.
func (ch *loginChild) succeeded() bool {
	return ch.cmd.ProcessState != nil && ch.cmd.ProcessState.Success()
}

// watch is the child's goroutine: it alone reports awaiting_code, failed and succeeded for it.
func (l *LoginRelay) watch(ctx context.Context, ch *loginChild) {
	defer l.wg.Done()
	defer close(ch.done)
	state, reason := l.drive(ctx, ch)
	ch.stop()
	// Wait has returned, so the output is complete: a head result drive did not take is zeroed now, not at GC.
	ch.out.dropHead()
	exit := -1
	if ch.cmd.ProcessState != nil {
		exit = ch.cmd.ProcessState.ExitCode()
	}
	l.Log.Info("login process ended", "attempt", ch.attempt, "exit", exit)
	ch.out.logCounts(l.Log)
	l.mu.Lock()
	if l.sess.child == ch {
		l.sess.child = nil
	}
	l.mu.Unlock()
	if state == "" || ch.stopped.Load() || ctx.Err() != nil {
		return
	}
	l.report(ctx, ch.session, ch.attempt, state, reason, loginrelay.Material{})
	if reason != loginrelay.ReasonInvalidCode {
		// A rejected code is followed by a new start: the session stays open.
		l.release(ch.session)
	}
}

// drive follows the child to its outcome. An empty state means there is nothing to report (stopped, or ctx ended).
func (l *LoginRelay) drive(ctx context.Context, ch *loginChild) (loginrelay.State, loginrelay.Reason) {
	t := l.timings
	urlWait := time.NewTimer(t.URL)
	defer urlWait.Stop()
	select {
	case <-ctx.Done():
		return "", ""
	case <-ch.exited:
		return loginrelay.StateFailed, loginrelay.ReasonProcessExited
	case <-urlWait.C:
		return loginrelay.StateFailed, loginrelay.ReasonNoURL
	case h := <-ch.out.head:
		if h.reason != "" {
			return loginrelay.StateFailed, h.reason
		}
		if ch.stopped.Load() {
			h.url.Zero()
			return "", ""
		}
		l.mu.Lock()
		ch.awaiting = true
		l.mu.Unlock()
		l.report(ctx, ch.session, ch.attempt, loginrelay.StateAwaitingCode, "", h.url)
		h.url.Zero()
	}

	if l.beforeCodeWait != nil {
		l.beforeCodeWait(ch)
	}
	select {
	case <-ctx.Done():
		return "", ""
	case <-ch.exited:
		// The code may have been written before the child exited, with both ready and the exit picked: then the exit
		// is judged like any other after a code.
		l.mu.Lock()
		written := ch.written
		l.mu.Unlock()
		if !written {
			return loginrelay.StateFailed, loginrelay.ReasonProcessExited
		}
	case <-ch.code:
	}
	if ch.loggedIn {
		return l.relogin(ctx, ch)
	}
	return l.confirm(ctx, ch)
}

// relogin settles an attempt while the login was ok before: only the exit code counts.
func (l *LoginRelay) relogin(ctx context.Context, ch *loginChild) (loginrelay.State, loginrelay.Reason) {
	deadline := time.NewTimer(l.timings.Relogin)
	defer deadline.Stop()
	select {
	case <-ctx.Done():
		return "", ""
	case <-ch.out.invalid:
		return loginrelay.StateFailed, loginrelay.ReasonInvalidCode
	case <-ch.exited:
		switch {
		case ch.out.rejected():
			return loginrelay.StateFailed, loginrelay.ReasonInvalidCode
		case ch.succeeded():
			return loginrelay.StateSucceeded, ""
		}
		return loginrelay.StateFailed, loginrelay.ReasonProcessExited
	case <-deadline.C:
		return loginrelay.StateFailed, loginrelay.ReasonTimeout
	}
}

// confirm settles an attempt while the login was missing before: the exit code with a following check, or a check
// that says ok while the child still runs, after which the child gets ExitGrace to exit by itself.
func (l *LoginRelay) confirm(ctx context.Context, ch *loginChild) (loginrelay.State, loginrelay.Reason) {
	deadline := time.NewTimer(l.timings.Submit)
	defer deadline.Stop()
	tick := time.NewTicker(l.timings.ConfirmEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ""
		case <-ch.out.invalid:
			return loginrelay.StateFailed, loginrelay.ReasonInvalidCode
		case <-ch.exited:
			switch {
			case ch.stopped.Load():
				return "", ""
			case ch.out.rejected():
				return loginrelay.StateFailed, loginrelay.ReasonInvalidCode
			case !ch.succeeded():
				return loginrelay.StateFailed, loginrelay.ReasonProcessExited
			case l.check(ctx) == provider.LoginOK:
				return loginrelay.StateSucceeded, ""
			}
			return loginrelay.StateFailed, loginrelay.ReasonNotConfirmed
		case <-tick.C:
			if l.check(ctx) != provider.LoginOK {
				continue
			}
			grace := time.NewTimer(l.timings.ExitGrace)
			select {
			case <-ch.exited:
			case <-grace.C:
			case <-ctx.Done():
				grace.Stop()
				return "", ""
			}
			grace.Stop()
			return loginrelay.StateSucceeded, ""
		case <-deadline.C:
			return loginrelay.StateFailed, loginrelay.ReasonTimeout
		}
	}
}

// headResult is what the start of stdout said: a URL that passed ValidateURL, or the reason it did not.
type headResult struct {
	url    loginrelay.Material
	reason loginrelay.Reason
}

// cliOutput reads both streams of the child. Before the URL, stdout goes into a buffer of at most maxLoginHead bytes
// that is cleared once the two lines are judged; everything else is dropped after a rolling-window look for the
// invalid-code phrase. Nothing of it is kept, logged or returned beyond a boolean and counts.
type cliOutput struct {
	head    chan headResult // one result
	invalid chan struct{}   // closed when the phrase was seen

	mu       sync.Mutex
	buf      []byte // stdout before the URL line
	off      int    // the start of the line being read in buf
	lines    int    // lines of the head judged
	judged   bool   // the head is over
	seen     bool   // the invalid-code phrase was seen
	tail     [2][]byte
	byteN    [2]int
	lineN    [2]int
	foundURL bool
}

func newCLIOutput() *cliOutput {
	return &cliOutput{
		head: make(chan headResult, 1), invalid: make(chan struct{}),
		buf: make([]byte, 0, maxLoginHead),
	}
}

// dropHead takes a head result nobody received, without blocking, and zeroes its URL.
func (o *cliOutput) dropHead() {
	select {
	case h := <-o.head:
		h.url.Zero()
	default:
	}
}

// rejected says whether the invalid-code phrase was seen.
func (o *cliOutput) rejected() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.seen
}

func (o *cliOutput) logCounts(log *slog.Logger) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, name := range []string{"stdout", "stderr"} {
		log.Debug("login output", "stream", name, "bytes", o.byteN[i], "lines", o.lineN[i],
			"url_line", i == 0 && o.foundURL, "invalid_code", o.seen)
	}
}

type streamWriter struct {
	out    *cliOutput
	stream int // 0 stdout, 1 stderr
}

// Write never fails and never blocks: a short write would fail the child.
func (w streamWriter) Write(p []byte) (int, error) {
	w.out.write(w.stream, p)
	return len(p), nil
}

func (o *cliOutput) write(stream int, p []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.byteN[stream] += len(p)
	o.lineN[stream] += bytes.Count(p, []byte{'\n'})
	if stream == 0 && !o.judged {
		p = o.readHead(p)
	}
	o.scan(stream, p)
}

// readHead takes stdout until the two lines of the head are judged and returns what follows them in p.
func (o *cliOutput) readHead(p []byte) []byte {
	take := min(len(p), maxLoginHead-len(o.buf))
	o.buf = append(o.buf, p[:take]...)
	rest := p[take:]
	for !o.judged {
		i := bytes.IndexByte(o.buf[o.off:], '\n')
		if i < 0 {
			break
		}
		line := o.buf[o.off : o.off+i]
		o.off += i + 1
		o.lines++
		switch o.lines {
		case 1:
			if !bytes.Equal(line, openingLine) {
				o.judge(headResult{reason: loginrelay.ReasonNoURL})
			}
		case 2:
			candidate, ok := bytes.CutPrefix(line, visitPrefix)
			switch {
			case !ok:
				o.judge(headResult{reason: loginrelay.ReasonNoURL})
			case loginrelay.ValidateURL(string(candidate)) != nil:
				o.judge(headResult{reason: loginrelay.ReasonBadURL})
			default:
				o.foundURL = true
				o.judge(headResult{url: loginrelay.NewMaterial(string(candidate))})
			}
		}
	}
	if o.judged {
		// What followed the URL line is dropped after the scan, like the rest of the output.
		o.scan(0, o.buf[o.off:])
		clear(o.buf)
		o.buf = nil
		return rest
	}
	if len(rest) > 0 || len(o.buf) >= maxLoginHead {
		o.judge(headResult{reason: loginrelay.ReasonNoURL})
		clear(o.buf)
		o.buf = nil
	}
	return rest
}

func (o *cliOutput) judge(r headResult) {
	o.judged = true
	o.head <- r // buffered for exactly this one result
}

// scan looks for the invalid-code phrase across writes, keeping only the last few bytes of each stream.
func (o *cliOutput) scan(stream int, p []byte) {
	if o.seen || len(p) == 0 {
		return
	}
	w := make([]byte, 0, len(o.tail[stream])+len(p))
	w = append(append(w, o.tail[stream]...), p...)
	if bytes.Contains(w, invalidPhrase) {
		o.seen = true
		close(o.invalid)
	}
	k := min(len(w), len(invalidPhrase)-1)
	clear(o.tail[stream])
	o.tail[stream] = append(o.tail[stream][:0], w[len(w)-k:]...)
	clear(w)
}
