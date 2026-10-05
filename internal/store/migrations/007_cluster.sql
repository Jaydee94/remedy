-- cluster: the run was started with cluster tools, the Kubernetes tools of the gatekeeper. It is set together with
-- mcp when the run is created and never changes; the gatekeeper offers the cluster tools only to such a run.
ALTER TABLE runs ADD COLUMN cluster INTEGER NOT NULL DEFAULT 0;
