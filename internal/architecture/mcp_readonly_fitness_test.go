package architecture

// Read-only MCP sensor (docs/adr/0008). The MCP server is an ADDITIVE,
// READ-ONLY inbound adapter: it can read departures and can never change one
// or emit an event. "Read-only" is pinned structurally, not by review:
//
//   - the adapter's Deps struct holds only interfaces whose every method is
//     one of the read use cases (ListDepartures, GetDeparture -- the same
//     queries GET /departures and GET /departures/{departureId} serve);
//   - the adapter and cmd/mcp import no write-capable seam (ports, the
//     outbox, an event sink, a unit of work, Kafka, the memory doubles) and
//     not even the REST adapter;
//   - the sources may name no usecases identifier outside the read surface;
//   - cmd/mcp's usecases.Deps literal sets none of the write-side fields
//     (UoW, Schedules, Loads, Nodes, Lanes, Volumes, Inbox, Outbox), so the
//     service instance it wires cannot reach a write path either.
//
// TestMCPReadOnlySensorBites proves each detector flags what it should.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// mcpReadUseCases is the whole set of use-case methods the MCP surface may
// hold or wire: exactly the reads the REST adapter exposes.
var mcpReadUseCases = map[string]bool{"ListDepartures": true, "GetDeparture": true}

// mcpAllowedUsecaseRefs are the usecases-package identifiers the MCP sources
// may refer to: the service constructor and its input/output types of the
// reads. Everything else named in package usecases is a write path (or a
// seam to one) and is refused.
var mcpAllowedUsecaseRefs = map[string]bool{
	"New": true, "Deps": true, "Service": true, "ListDeparturesQuery": true,
}

// mcpForbiddenImportParts are import-path fragments no MCP source may carry:
// the write-capable seams and every moving part of the write side. The ports
// package is NOT banned by path (it also holds the read side's ErrNotFound);
// it is policed identifier-by-identifier by mcpPortsRefViolations below.
var mcpForbiddenImportParts = []string{
	"/internal/application/outbox",
	"/internal/adapters/outbound/outbox",
	"/internal/adapters/outbound/kafka",
	"/internal/adapters/outbound/memory",
	"/internal/adapters/inbound/kafka",
	"/internal/adapters/inbound/http",
	"/internal/adapters/kafka",
	"segmentio/kafka-go",
}

// mcpAllowedPortsRefs are the only identifiers of package ports an MCP source
// may name: the read side's not-found error. Everything else in ports is a
// write seam (UnitOfWork, EventOutbox, the repositories' Save side).
var mcpAllowedPortsRefs = map[string]bool{"ErrNotFound": true}

// mcpForbiddenDepsFields are the write-side fields of usecases.Deps that
// cmd/mcp's composite literal must not set.
var mcpForbiddenDepsFields = map[string]bool{
	"UoW": true, "Schedules": true, "Loads": true, "Nodes": true, "Lanes": true,
	"Volumes": true, "Inbox": true, "Outbox": true,
}

// mcpImportViolations returns one finding per forbidden import of one source.
func mcpImportViolations(filename string, src any) ([]string, error) {
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
		for _, part := range mcpForbiddenImportParts {
			if strings.Contains(path, part) {
				out = append(out, fmt.Sprintf("%s imports %s", filename, path))
			}
		}
	}
	return out, nil
}

// mcpInterfaceMethods collects, over every source, the method names of every
// interface declared in package mcp itself.
func mcpInterfaceMethods(filename string, src any) (map[string][]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			it, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}
			methods := []string{}
			for _, field := range it.Methods.List {
				if len(field.Names) > 0 {
					methods = append(methods, field.Names[0].Name)
				}
			}
			out[ts.Name.Name] = methods
		}
	}
	return out, nil
}

// mcpIsReadInterface reports whether e is an identifier naming an interface
// declared in the adapter whose every method is a read use case.
func mcpIsReadInterface(interfaces map[string][]string, e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	methods, declared := interfaces[id.Name]
	if !declared {
		return false
	}
	for _, m := range methods {
		if !mcpReadUseCases[m] {
			return false
		}
	}
	return true
}

// mcpDepsFieldViolations returns every field of the struct named Deps whose
// type is not a package-local interface whose methods are all read use cases.
func mcpDepsFieldViolations(interfaces map[string][]string, filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "Deps" {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		out = append(out, mcpNonReadFieldNames(interfaces, st, filename)...)
		return false
	})
	return out, nil
}

// mcpNonReadFieldNames names every field of one Deps struct that is not held
// through a local read interface.
func mcpNonReadFieldNames(interfaces map[string][]string, st *ast.StructType, filename string) []string {
	var out []string
	for _, field := range st.Fields.List {
		if mcpIsReadInterface(interfaces, field.Type) {
			continue
		}
		names := make([]string, 0, len(field.Names))
		for _, n := range field.Names {
			names = append(names, n.Name)
		}
		out = append(out, fmt.Sprintf("%s: Deps field %s is not a local read interface (a local interface whose every method is a read use case)", filename, strings.Join(names, ",")))
	}
	return out
}

// mcpUsecaseRefViolations returns every usecases.<Name> a source refers to
// that is not in mcpAllowedUsecaseRefs (the MCP sources must not even name a
// write use case).
func mcpUsecaseRefViolations(filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "usecases" && !mcpAllowedUsecaseRefs[sel.Sel.Name] {
			out = append(out, fmt.Sprintf("%s refers to usecases.%s, which is not a read use case or its input/output", filename, sel.Sel.Name))
		}
		return true
	})
	return out, nil
}

// mcpPortsRefViolations returns every ports.<Name> a source refers to that is
// not in mcpAllowedPortsRefs: the ports package is the write seam, and the
// read side may touch only its not-found sentinel.
func mcpPortsRefViolations(filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "ports" && !mcpAllowedPortsRefs[sel.Sel.Name] {
			out = append(out, fmt.Sprintf("%s refers to ports.%s, which is a write seam of the ports package", filename, sel.Sel.Name))
		}
		return true
	})
	return out, nil
}

// mcpWiredDepsViolations returns every write-side field a usecases.Deps
// composite literal in src sets.
func mcpWiredDepsViolations(filename string, src any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Deps" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "usecases" {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && mcpForbiddenDepsFields[key.Name] {
				out = append(out, fmt.Sprintf("%s wires usecases.Deps.%s, a write-side seam", filename, key.Name))
			}
		}
		return true
	})
	return out, nil
}

func mcpReadOnlyFiles(t *testing.T) (adapter, command []string) {
	t.Helper()
	adapter = goFilesUnder(t, "../adapters/inbound/mcp", false)
	command = goFilesUnder(t, filepath.Join("..", "..", "cmd", "mcp"), false)
	if len(adapter) == 0 || len(command) == 0 {
		t.Fatalf("no non-test Go file under the MCP adapter (%d) or cmd/mcp (%d): the sensor would pass vacuously", len(adapter), len(command))
	}
	sort.Strings(adapter)
	sort.Strings(command)
	return adapter, command
}

// mcpRunDetector applies one detector to every path and fails the test with
// why for each finding.
func mcpRunDetector(t *testing.T, why string, det func(string, any) ([]string, error), paths ...string) {
	t.Helper()
	for _, path := range paths {
		violations, err := det(path, nil)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, v := range violations {
			t.Errorf("READ-ONLY MCP VIOLATION: %s -- %s (docs/adr/0008)", v, why)
		}
	}
}

// TestMCPServerIsReadOnly: the MCP adapter and cmd/mcp can reach no write
// path.
func TestMCPServerIsReadOnly(t *testing.T) {
	adapter, command := mcpReadOnlyFiles(t)

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
	mcpRunDetector(t, "the MCP surface may read through the use cases only", mcpImportViolations, all...)
	depsFields := func(filename string, src any) ([]string, error) {
		return mcpDepsFieldViolations(interfaces, filename, src)
	}
	mcpRunDetector(t, "the MCP adapter may hold read interfaces only", depsFields, adapter...)
	mcpRunDetector(t, "name only the read use cases and their inputs/outputs", mcpUsecaseRefViolations, all...)
	mcpRunDetector(t, "of the ports package only the not-found error is readable", mcpPortsRefViolations, all...)
	mcpRunDetector(t, "cmd/mcp must wire the read repository only, never a write seam", mcpWiredDepsViolations, command...)
}

// mcpBite is one sensor self-test case: src must produce want findings.
type mcpBite struct {
	name string
	src  string
	want int
}

// mcpExpectBites runs one detector over every fixture and fails on a count
// that does not bite (or bites what should pass).
func mcpExpectBites(t *testing.T, det func(string, any) ([]string, error), cases []mcpBite) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := det("src.go", tc.src)
			if err != nil || len(got) != tc.want {
				t.Fatalf("findings = %v (err %v), want %d", got, err, tc.want)
			}
		})
	}
}

func TestMCPReadOnlySensorBites(t *testing.T) {
	mcpExpectBites(t, mcpImportViolations, []mcpBite{
		{"the outbox relay is flagged", "package x\nimport \"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/outbox\"", 1},
		{"a kafka consumer is flagged", "package x\nimport \"github.com/claudioed/linehaul-transportation/internal/adapters/inbound/kafka\"", 1},
		{"the REST adapter is flagged", "package x\nimport \"github.com/claudioed/linehaul-transportation/internal/adapters/inbound/http\"", 1},
		{"kafka-go is flagged", "package x\nimport \"github.com/segmentio/kafka-go\"", 1},
		{"the memory doubles are flagged", "package x\nimport \"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/memory\"", 1},
		{"usecases passes", "package x\nimport \"github.com/claudioed/linehaul-transportation/internal/application/usecases\"", 0},
		{"ports passes by path (its identifiers are policed separately)", "package x\nimport \"github.com/claudioed/linehaul-transportation/internal/application/ports\"", 0},
		{"the postgres adapter passes", "package x\nimport \"github.com/claudioed/linehaul-transportation/internal/adapters/outbound/postgres\"", 0},
	})

	ifaces := map[string][]string{"DepartureReader": {"ListDepartures", "GetDeparture"}}
	depsFields := func(filename string, src any) ([]string, error) {
		return mcpDepsFieldViolations(ifaces, filename, src)
	}
	mcpExpectBites(t, depsFields, []mcpBite{
		{"the read interface passes", "package x\n type DepartureReader interface {\n ListDepartures()\n GetDeparture()\n }\n type Deps struct{ Departures DepartureReader }", 0},
		{"a write method on the interface is flagged", "package x\ntype W interface{ CancelDeparture() }\ntype Deps struct{ W W }", 1},
		{"a concrete struct field is flagged", "package x\ntype DepartureReader interface{ GetDeparture() }\ntype Deps struct{ S Service }", 1},
		{"a pointer to a use case is flagged", "package x\ntype Deps struct{ S *usecases.Service }", 1},
		{"other structs are not Deps", "package x\ntype Other struct{ S *usecases.Service }", 0},
	})

	mcpExpectBites(t, mcpUsecaseRefViolations, []mcpBite{
		{"constructing a write use case reference is flagged", "package main\nvar _ = usecases.Service{}\nvar _ = usecases.OpenLoad{}", 1},
		{"read inputs pass", "package main\nvar _ usecases.ListDeparturesQuery\nvar _ *usecases.Service\nvar _ = usecases.New\nvar _ usecases.Deps", 0},
	})

	mcpExpectBites(t, mcpWiredDepsViolations, []mcpBite{
		{"the read repository passes", "package main\nvar _ = usecases.Deps{Departures: r}", 0},
		{"wiring the unit of work is flagged", "package main\nvar _ = usecases.Deps{UoW: u}", 1},
		{"wiring the outbox is flagged", "package main\nvar _ = usecases.Deps{Outbox: o}", 1},
		{"wiring a second repository is flagged", "package main\nvar _ = usecases.Deps{Loads: l}", 1},
		{"other literals are not checked", "package main\nvar _ = http.Server{Handler: h}", 0},
	})

	mcpExpectBites(t, mcpPortsRefViolations, []mcpBite{
		{"the read sentinel passes", "package x\nvar _ = ports.ErrNotFound", 0},
		{"the unit of work is flagged", "package x\nvar _ ports.UnitOfWork", 1},
		{"the outbox port is flagged", "package x\nvar _ ports.EventOutbox", 1},
		{"a save-side repository is flagged", "package x\nvar _ ports.LoadRepository", 1},
	})
}
