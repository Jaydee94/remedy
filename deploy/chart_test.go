package deploy_test

import "testing"

func TestTheRenderNeedsAnExistingSecret(t *testing.T) {
	values := baseValues()
	set(values, "existingSecret.name", "")
	mustFail(t, values, "existingSecret.name")
}
