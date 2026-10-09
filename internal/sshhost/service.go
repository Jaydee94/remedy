package sshhost

import (
	"context"
	"sync"
	"time"
)

// testCommands are the four commands of a connection test. They are constants: no user input ever reaches them.
var testCommands = [4]string{"id -un", "id -Gn", "uname -sr", "env LC_ALL=C sudo -n -l"}

// Service is what the control plane uses. The zero value is ready. It lets only one connection per host be open at a time,
// so two clicks on "Test" do not open two sessions.
type Service struct {
	mu    sync.Mutex
	locks map[int64]*sync.Mutex
}

func (s *Service) lock(hostID int64) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[int64]*sync.Mutex{}
	}
	m := s.locks[hostID]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[hostID] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// Probe returns the key a host presents. See the package function.
func (s *Service) Probe(ctx context.Context, hostID int64, t Target) (HostKey, error) {
	defer s.lock(hostID)()
	return Probe(ctx, t)
}

// Check connects against the pinned key, runs the four test commands and returns the report.
func (s *Service) Check(ctx context.Context, hostID int64, t Target, cred Credential, pinLine string) (Report, error) {
	defer s.lock(hostID)()
	cl, err := Dial(ctx, t, cred, pinLine)
	if err != nil {
		return Report{}, err
	}
	defer cl.Close()
	var res [len(testCommands)]Result
	for i, cmd := range testCommands {
		if res[i], err = cl.Run(ctx, cmd); err != nil {
			return Report{}, err
		}
	}
	return BuildReport(time.Now(), Outcome{
		Account: res[0].Output, GroupsOut: res[1].Output, System: res[2].Output, SudoOut: res[3].Output, SudoOK: res[3].ExitCode == 0,
	}), nil
}
