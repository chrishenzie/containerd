/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package podsandbox

import (
	"context"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/internal/cri/nri"
	sstore "github.com/containerd/containerd/v2/internal/cri/store/sandbox"
	"github.com/containerd/log"
)

// WithNRISandboxDelete calls delete for a sandbox'd task
func WithNRISandboxDelete(nri *nri.API, sandboxID string) containerd.ProcessDeleteOpts {
	return func(ctx context.Context, p containerd.Process) error {
		task, ok := p.(containerd.Task)
		if !ok {
			return nil
		}
		if nri == nil || nri.IsDisabled() {
			return nil
		}
		sb := &sstore.Sandbox{
			Metadata: sstore.Metadata{ID: sandboxID},
		}
		defer nri.BlockPluginSync().Unblock()
		if err := nri.RemovePodSandbox(ctx, sb); err != nil {
			log.G(ctx).WithError(err).Errorf("Failed to remove pod sandbox in nri for %q", task.ID())
		}
		return nil
	}
}
