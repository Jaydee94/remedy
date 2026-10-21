# Runbook: skills

A skill is a short note about your lab, with a name, a one-line description and a body of plain text, bound to one host or global ("the NAS keeps
Paperless in `/volume1/docker`", "node_exporter on this host fails when the old instance still holds the port: a restart is safe"). This is cycle
5 of the hosts and skills series ([spec](../specs/2026-10-19-skills-5-design.md)). You write skills yourself, and a read-only **learning run** can propose
a profile and skills for a host; those wait in the review list (section 4) until you confirm them. The real run of the learning run against the real CLI is
described in [skills-real-run.md](skills-real-run.md).

## 1. Write one

Open **Skills** and press **New skill**.

- **Where it applies**: **Global** (listed for every run with tools) or one host (listed only for runs that have that host: the incident's host
  in an automatic diagnosis, the hosts you chose on the Ask page).
- **Name**: lower case letters, digits and dashes, 1 to 40 characters, starting with a letter or a digit. It is what the agent passes to the tool
  `skill_get`; it is unique within the scope.
- **Description**: one line, at most 200 characters, that says what the note is about. This is what the agent reads in its prompt to decide whether to
  load the note, so write it for that.
- **Note**: at most 8 KB of plain text. Markdown is kept as you type it and is shown as text, never rendered. A secret you paste here is stored as typed
  and removed whenever a note is handed to an agent, but do not keep secrets in notes.

A scope holds at most 30 skills. A skill you save is on at once.

## 2. What a run sees

A run with gatekeeper access gets, in its prompt, the names and descriptions of the skills that are **on** and either global or bound to a host the
run has (at most 40 lines; a line says how many were left out). The note itself is loaded by the agent with `skill_get` when the description fits.
A run without tools (the GitHub responder) gets no list. The list is read when the run starts (an automatic diagnosis: it is part of the stored prompt)
or when a runner claims it (an ad-hoc run); `skill_get` reads the current state, so a skill you turn off during a run can no longer be loaded.
When two scopes list the same name, `skill_get` takes the host's name, or `(global)`, to say which one is meant.

**A skill informs the agent and nothing else.** It cannot widen what `host_read` runs (that table is in the code), cannot remove an approval and cannot
change a limit. The prompt tells the agent so, and a test proves it: a note that says "docker rm is fine now" changes nothing, and a `host_change`
still waits for you under **Needs you** with the command word for word.

## 3. Turn off, edit, delete

On the list, the switch turns a skill on or off (off keeps it and hides it from every run), **Edit** changes name, description and note (not the scope),
**Delete** asks twice. A skill that is a proposal is not edited or deleted in this list: it is confirmed or rejected.

Deleting a host deletes its skills and the proposals that wait for it. The host page names them before the second click, and the activity feed says how many went.

## 4. Get to know a host, and the proposals

**Get to know this host.** On a host's page the button **Get to know this host** starts one run that reads the host with the host tools (`host_list`, `host_read`) and proposes what
it found. It is never automatic: it costs one run of your subscription, and the page says so. The run can only read, with the fixed commands that `host_read` allows;
it has no way to change anything or to store anything itself.

- The button works when the host is turned on, has a login and a pinned key, and its last connection test worked. After you pin a key or change the login, **test the
  connection again**. When it does not work, the page says why.
- Only one run goes at a time: another run, or a learning run for this host, blocks it. It is also blocked while 12 or more proposals of this host wait for your review
  (review or reject some first): at most 20 proposals wait per host.
- It proposes a **profile** (what the host is: operating system, role, services, containers, notes; Remedy writes them into one note named `profile` with fixed headings) and at most 8
  **skills**, each one fact or habit of the host, with the commands that showed it. Everything is a proposal. A skill with the name of one the host already has (also one you turned
  off) replaces that skill when you confirm it; a new profile replaces the confirmed one. A skill you turned off is not shown to the learning run either.
- If the run fails, times out, is cancelled (**Cancel run** on its page), loses its runner or gives an answer that is not valid, nothing is stored; the host's page says so and the
  activity feed has a line. The run is listed on the Ask page as `Learning about NAME`; its prompt is not shown there.

**The review list.** A proposal from an agent is invisible to every run
until you confirm it. **Waiting for your review** shows each one: the host, the name, the description, the note as text, and **the commands that produced
the facts** (at most ten, redacted). A command that the learning run did not actually run is marked "not run": do not trust a fact that rests on it. A
proposal that replaces a skill shows the current text beside the new one.

- **Confirm** makes it a skill that runs see; it keeps the origin "proposed by an agent, confirmed on DATE". A proposal that replaces a skill overwrites
  that skill's text with the proposal's.
- **Edit and confirm** changes the description and the note first.
- **Reject** deletes it; the activity feed keeps that it existed.

A proposal whose name was taken meanwhile cannot be confirmed (reject it, or rename or delete the other skill); a new skill in a scope with 30 skills cannot
either. Read the note before you confirm it: it comes from an agent that read the output of a host, and anyone who can write to that output can write
into the note.

## 5. When something is refused

| Message | Why |
|---|---|
| A skill with this name already exists in this scope. | Names are unique per scope; the same name for another host or global is fine. |
| This scope already has 30 skills. Delete one first. | A scope holds at most 30 skills. |
| A proposal can only be confirmed or rejected. | A proposal is not edited, deleted or switched on directly. |
| This skill is not waiting for a review. | Only a proposal is confirmed or rejected. |
| The description is one line of plain text... | Line breaks, control characters and invisible characters (zero-width, right-to-left marks, variation selectors) are refused in a description and in a note. |
| the host NAME is not ready: it is disabled / it has no login / its key is not pinned / its last connection test did not succeed; test the connection first | The learning run needs a host that is on, with a login, a pinned key and a successful test. |
| a learning run for this host is already queued or running | One learning run per host at a time. |
| another run is queued or running; try again when it has finished | A learning run starts only when no other run is queued or running. |
| too many proposals of this host wait for your review; confirm or reject some on the Skills page first | At most 20 proposals wait per host; a new run needs room for a profile and 8 skills. |
