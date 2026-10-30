package architecture

// A2A inbound adapter sensors (docs/adr/0011). The A2A agent is an ADDITIVE,
// READ-ONLY inbound adapter derived from the MCP charter (ADR 0008): it reads
// departures and can never change one or emit an event, and it delegates to
// no sibling. These sensors pin that structurally, reusing the MCP read-only
// detectors (the allowlists are the same two reads) and adding the one thing
// MCP has no counterpart for: no outbound A2A client.

import (
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	archgo "github.com/arch-go/arch-go/api"
	"github.com/arch-go/arch-go/api/configuration"
)

// TestA2AAdapterDependencyRule: the A2A adapter is additive, never
// load-bearing. It may depend only on the application and domain layers
// (never on outbound adapters, never on cmd), and nothing else in this
// codebase may depend on it; cmd/a2a wires it up and nothing else touches it.
func TestA2AAdapterDependencyRule(t *testing.T) {
	moduleInfo := configuration.Load(modulePath)

	t.Run("a2a adapter depends only on application and domain", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.adapters.inbound.a2a.**",
					ShouldOnlyDependsOn: &configuration.Dependencies{
						Internal: []string{
							"**.internal.adapters.inbound.a2a.**",
							"**.internal.application.**",
							"**.internal.domain.**",
						},
					},
				},
			},
		})
		assertArchGoPasses(t, result)
	})

	t.Run("nothing else depends on the a2a adapter", func(t *testing.T) {
		result := archgo.CheckArchitecture(moduleInfo, configuration.Config{
			DependenciesRules: []*configuration.DependenciesRule{
				{
					Package: "**.internal.**",
					ShouldNotDependsOn: &configuration.Dependencies{
						Internal: []string{"**.internal.adapters.inbound.a2a.**"},
					},
				},
			},
		})
		assertArchGoPasses(t, result)
	})
}

// a2aClientImportParts are the import paths of the outbound A2A client: this
// context calls no sibling over A2A (ADR 0011), so neither the adapter nor
// cmd/a2a may import it (the wire tests, which drive the agent as a client,
// are excluded from the scan).
var a2aClientImportParts = []string{"a2a-go/v2/a2aclient"}

// a2aClientViolations returns one finding per outbound-client import of one
// source.
func a2aClientViolations(filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		for _, part := range a2aClientImportParts {
			if strings.Contains(path, part) {
				out = append(out, fmt.Sprintf("%s imports %s", filename, path))
			}
		}
	}
	return out, nil
}

func a2aReadOnlyFiles(t *testing.T) (adapter, command []string) {
	t.Helper()
	adapter = goFilesUnder(t, "../adapters/inbound/a2a", false)
	command = goFilesUnder(t, filepath.Join("..", "..", "cmd", "a2a"), false)
	if len(adapter) == 0 || len(command) == 0 {
		t.Fatalf("no non-test Go file under the A2A adapter (%d) or cmd/a2a (%d): the sensor would pass vacuously", len(adapter), len(command))
	}
	sort.Strings(adapter)
	sort.Strings(command)
	return adapter, command
}

// TestA2AAdapterIsReadOnly: the A2A adapter and cmd/a2a can reach no write
// path and hold no outbound A2A client.
func TestA2AAdapterIsReadOnly(t *testing.T) {
	adapter, command := a2aReadOnlyFiles(t)

	interfaces := map[string][]string{}
	for _, path := range adapter {
		found, err := mcpInterfaceMethods(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for name, methods := range found {
			interfaces[name] = methods
		}
	}

	all := append(append([]string{}, adapter...), command...)
	run := func(why string, det func(string, any) ([]string, error), paths ...string) {
		t.Helper()
		for _, path := range paths {
			violations, err := det(path, nil)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, v := range violations {
				t.Errorf("READ-ONLY A2A VIOLATION: %s -- %s (docs/adr/0011)", v, why)
			}
		}
	}
	run("the A2A surface may read through the use cases only", mcpImportViolations, all...)
	run("this context calls no sibling over A2A: no outbound client", a2aClientViolations, all...)
	run("the A2A adapter may hold read interfaces only", func(filename string, src any) ([]string, error) {
		return mcpDepsFieldViolations(interfaces, filename, src)
	}, adapter...)
	run("name only the read use cases and their inputs/outputs", mcpUsecaseRefViolations, all...)
	run("of the ports package only the not-found error is readable", mcpPortsRefViolations, all...)
	run("cmd/a2a must wire the read repository only, never a write seam", mcpWiredDepsViolations, command...)
}

func TestA2AReadOnlySensorBites(t *testing.T) {
	mcpExpectBites(t, a2aClientViolations, []mcpBite{
		{"the a2a client is flagged", "package x\nimport \"github.com/a2aproject/a2a-go/v2/a2aclient\"", 1},
		{"the server package passes", "package x\nimport \"github.com/a2aproject/a2a-go/v2/a2asrv\"", 0},
		{"the types package passes", "package x\nimport \"github.com/a2aproject/a2a-go/v2/a2a\"", 0},
	})
	mcpExpectBites(t, mcpImportViolations, []mcpBite{
		{"the outbox relay is flagged", "package x\nimport \"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/outbox\"", 1},
		{"kafka is flagged", "package x\nimport \"github.com/claudioed/linehaul-transportation/internal/adapters/inbound/kafka\"", 1},
	})
	mcpExpectBites(t, mcpWiredDepsViolations, []mcpBite{
		{"the read repository passes", "package main\nvar _ = usecases.Deps{Departures: r}", 0},
		{"wiring the unit of work is flagged", "package main\nvar _ = usecases.Deps{UoW: u}", 1},
		{"wiring the outbox is flagged", "package main\nvar _ = usecases.Deps{Outbox: o}", 1},
	})
}
