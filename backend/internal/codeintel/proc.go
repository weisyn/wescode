package codeintel

import (
	"os/exec"
	"time"
)

// workerWaitDelay bounds how long Wait may block after cancellation before
// exec force-closes the descriptors the child inherited.
const workerWaitDelay = 5 * time.Second

// setCancellation installs both halves of the cancellation contract together,
// so a new spawn site cannot pick up one and miss the other.
//
// Cancel aims the kill at the descendant tree rather than the direct child.
// WaitDelay is the other half, and it is what carries the guarantee on Windows:
// there are no process groups there, so walking the tree is taskkill's job, and
// a grandchild that outlives taskkill while holding the stdout write end leaves
// Wait blocked with no deadline. A leaked grandchild costs a handle; a Wait that
// never returns stalls indexing for the rest of the session.
//
// Both fields must be set before Start — exec decides at Start time whether to
// spawn the context watcher at all, and that decision reads exactly these two.
func setCancellation(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return killGroup(cmd.Process.Pid)
	}
	cmd.WaitDelay = workerWaitDelay
}
