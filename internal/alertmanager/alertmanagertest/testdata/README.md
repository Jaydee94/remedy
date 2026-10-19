# testdata

`home-server.json` is a hand-written sample, modelled on the documentation of Alertmanager's v2 API and on the labels of the home server; it is not a recording of the home server's answer, and its fingerprints and times are invented.
`alertmanager-0.34.1.json` is the unedited answer of a real Alertmanager 0.34.1 (a container, recorded in the real run of
`docs/research/phase-2d-real-run.md`) to the request the client sends, for two alerts that were posted to its API; the alerts are made up, the shape of
the answer is real (it has the member `mutedBy`, which the hand-written sample does not). A recording of the home server's own answer is still missing.
