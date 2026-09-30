package main

import (
	"context"

	"github.com/etalab-ia/just-code/internal/justcode"
)

// projectGitHubRemote returns no remote unless the project has the host-local
// P09 GitHub approval. A public origin by itself must not activate the token.
func projectGitHubRemote(projectRoot, stateDir, instance string) (justcode.GitHubRemote, error) {
	approvals, err := justcode.ReadBindingApprovals(
		justcode.DefaultFS,
		justcode.BindingApprovalsPath(stateDir, instance),
	)
	if err != nil || !approvals.Approves(justcode.CredentialGithub) {
		return justcode.GitHubRemote{}, nil
	}
	return justcode.GitHubOriginForProject(context.Background(), projectRoot)
}
