package tools_test

import (
	"testing"

	"github.com/altairalabs/codegen-sandbox/internal/tools"
)

// TestRegister_AllToolsAddTheirSchema is a coverage sweep over every
// public Register function. The handlers are exercised individually
// elsewhere (file-by-file in *_test.go); this test is purely about
// making sure the registration metadata path runs at least once so
// SonarCloud's new-code coverage reflects the schema wiring (the
// `mcp.NewTool(...)` + `mcp.WithDescription / WithString / WithNumber`
// chains added by the multi-workspace pass).
//
// Each Register function is allowed to mutate the registrar however
// it likes; the test only requires that calling it doesn't panic and
// that it adds at least one tool name.
func TestRegister_AllToolsAddTheirSchema(t *testing.T) {
	deps, _ := newTestDeps(t)
	deps.Shells = tools.NewShellRegistry()

	registrars := []struct {
		name string
		fn   func(tools.ToolAdder, *tools.Deps)
	}{
		{"Read", tools.RegisterRead},
		{"Write", tools.RegisterWrite},
		{"Edit", tools.RegisterEdit},
		{"Glob", tools.RegisterGlob},
		{"Grep", tools.RegisterGrep},
		{"Bash", tools.RegisterBash},
		{"BashOutput", tools.RegisterBashOutput},
		{"KillShell", tools.RegisterKillShell},
		{"RunTests", tools.RegisterRunTests},
		{"RunLint", tools.RegisterRunLint},
		{"RunTypecheck", tools.RegisterRunTypecheck},
		{"RunScript", tools.RegisterRunScript},
		{"LastTestFailures", tools.RegisterLastTestFailures},
		{"RunFailingTests", tools.RegisterRunFailingTests},
		{"TestsCovering", tools.RegisterTestsCovering},
		{"Snapshots", tools.RegisterSnapshots},
		{"SnapshotsReadOnly", tools.RegisterSnapshotsReadOnly},
		{"SnapshotsMutating", tools.RegisterSnapshotsMutating},
		{"SearchCode", tools.RegisterSearchCode},
		{"ASTEdits", tools.RegisterASTEdits},
		{"LSPTools", tools.RegisterLSPTools},
		{"LSPNavigation", tools.RegisterLSPNavigation},
		{"LSPRename", tools.RegisterLSPRename},
		{"Secrets", tools.RegisterSecrets},
		{"Render", tools.RegisterRender},
		{"WatchProcess", tools.RegisterWatchProcess},
		{"WatchProcessEvents", tools.RegisterWatchProcessEvents},
	}
	for _, tc := range registrars {
		t.Run(tc.name, func(t *testing.T) {
			reg := &fakeToolRegistrar{}
			tc.fn(reg, deps)
			if len(reg.handlers) == 0 {
				t.Fatalf("%s: registered no tools", tc.name)
			}
		})
	}
}
