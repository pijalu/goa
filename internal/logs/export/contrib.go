// SPDX-License-Identifier: GPL-3.0-or-later

package export

// Artifact is a single diagnostic file contributed to a bundle. Exactly one of
// Data or Path is set: Data is written verbatim; Path is copied from a source
// file. Name is the destination path inside the bundle.
type Artifact struct {
	Name string
	Data []byte
	Path string
}

// ArtifactContributor returns extra diagnostic artifacts rooted at projectDir.
// It is the Open/Closed extension point of the bundler: each subsystem that
// owns diagnostics outside the main session store (e.g. the orchestrator run
// log) registers a contributor rather than teaching this package its format.
type ArtifactContributor func(projectDir string) []Artifact

// contributors is the package-level registry of artifact sources. It is the
// single seam through which subsystems plug into the export bundle, keeping
// this package free of domain knowledge (Single Responsibility + Open/Closed).
var contributors []ArtifactContributor

// RegisterContributor adds an artifact source. It is intended to be called
// once from the composition root (package init) of the subsystem that owns the
// artifacts. Nil contributors are ignored.
func RegisterContributor(c ArtifactContributor) {
	if c == nil {
		return
	}
	contributors = append(contributors, c)
}

// registeredContributors returns a snapshot of the registry for safe iteration
// (callers may register concurrently in tests).
func registeredContributors() []ArtifactContributor {
	out := make([]ArtifactContributor, len(contributors))
	copy(out, contributors)
	return out
}
