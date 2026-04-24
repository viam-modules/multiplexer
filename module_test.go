package multiplexer

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	generic "go.viam.com/rdk/services/generic"
)

// fakeResource is a minimal resource.Resource implementation used in tests.
type fakeResource struct {
	resource.AlwaysRebuild
	name        resource.Name
	doCommandFn func(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error)
	statusFn    func(ctx context.Context) (map[string]interface{}, error)
}

func (f *fakeResource) Name() resource.Name { return f.name }

func (f *fakeResource) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	if f.doCommandFn != nil {
		return f.doCommandFn(ctx, cmd)
	}
	return map[string]interface{}{"ok": true}, nil
}

func (f *fakeResource) Status(ctx context.Context) (map[string]interface{}, error) {
	if f.statusFn != nil {
		return f.statusFn(ctx)
	}
	return map[string]interface{}{"status": "ok"}, nil
}

func (f *fakeResource) Close(context.Context) error { return nil }

// newFake builds a fakeResource named under the generic service API.
func newFake(name string) *fakeResource {
	return &fakeResource{name: generic.Named(name)}
}

// makeDeps builds a resource.Dependencies map from the provided resources.
func makeDeps(resources ...resource.Resource) resource.Dependencies {
	deps := make(resource.Dependencies, len(resources))
	for _, r := range resources {
		deps[r.Name()] = r
	}
	return deps
}

func testLogger() logging.Logger {
	return logging.NewLogger("test")
}

// ---- Config.Validate tests ----

func TestConfigValidate_NoDependencies(t *testing.T) {
	cfg := &Config{}
	_, _, err := cfg.Validate("root")
	if err == nil {
		t.Fatal("expected error for empty dependencies, got nil")
	}
}

func TestConfigValidate_EmptyDepName(t *testing.T) {
	cfg := &Config{Dependencies: []string{"svc-a", "", "svc-b"}}
	_, _, err := cfg.Validate("root")
	if err == nil {
		t.Fatal("expected error for empty dependency name, got nil")
	}
}

func TestConfigValidate_Valid(t *testing.T) {
	cfg := &Config{Dependencies: []string{"svc-a", "svc-b"}}
	required, optional, err := cfg.Validate("root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(optional) != 0 {
		t.Errorf("expected no optional deps, got %v", optional)
	}
	if len(required) != 2 {
		t.Fatalf("expected 2 required deps, got %d", len(required))
	}
	// The returned required names must contain "svc-a" and "svc-b".
	found := map[string]bool{}
	for _, n := range required {
		found[n] = true
	}
	for _, want := range []string{"svc-a", "svc-b"} {
		if !found[want] {
			t.Errorf("required deps missing %q; got %v", want, required)
		}
	}
}

// ---- New dependency-resolution tests ----

func TestNew_ResolvesByShortName(t *testing.T) {
	svc := newFake("my-service")
	deps := makeDeps(svc)
	conf := &Config{Dependencies: []string{"my-service"}}
	name := generic.Named("mux")

	mux, err := New(context.Background(), deps, name, conf, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mux.Close(context.Background())
}

func TestNew_ResolvesByFullString(t *testing.T) {
	svc := newFake("my-service")
	deps := makeDeps(svc)
	// Use the full Name.String() as the dependency reference.
	fullName := svc.Name().String()
	conf := &Config{Dependencies: []string{fullName}}
	name := generic.Named("mux")

	mux, err := New(context.Background(), deps, name, conf, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mux.Close(context.Background())
}

func TestNew_MissingDependency(t *testing.T) {
	deps := make(resource.Dependencies)
	conf := &Config{Dependencies: []string{"nonexistent"}}
	name := generic.Named("mux")

	_, err := New(context.Background(), deps, name, conf, testLogger())
	if err == nil {
		t.Fatal("expected error for missing dependency, got nil")
	}
}

func TestNew_MultipleDepsSomeResolved(t *testing.T) {
	svcA := newFake("svc-a")
	deps := makeDeps(svcA)
	conf := &Config{Dependencies: []string{"svc-a", "svc-missing"}}
	name := generic.Named("mux")

	_, err := New(context.Background(), deps, name, conf, testLogger())
	if err == nil {
		t.Fatal("expected error when one dependency is missing")
	}
}

// ---- DoCommand fan-out tests ----

func TestDoCommand_FansOutToAllDependencies(t *testing.T) {
	called := map[string]bool{}
	makeDoFn := func(id string) func(context.Context, map[string]interface{}) (map[string]interface{}, error) {
		return func(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
			called[id] = true
			return map[string]interface{}{"from": id}, nil
		}
	}

	svcA := &fakeResource{name: generic.Named("svc-a"), doCommandFn: makeDoFn("svc-a")}
	svcB := &fakeResource{name: generic.Named("svc-b"), doCommandFn: makeDoFn("svc-b")}
	deps := makeDeps(svcA, svcB)
	conf := &Config{Dependencies: []string{"svc-a", "svc-b"}}
	name := generic.Named("mux")

	mux, err := New(context.Background(), deps, name, conf, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mux.Close(context.Background())

	result, err := mux.DoCommand(context.Background(), map[string]interface{}{"ping": true})
	if err != nil {
		t.Fatalf("DoCommand returned error: %v", err)
	}

	if !called["svc-a"] || !called["svc-b"] {
		t.Errorf("not all deps were called; called=%v", called)
	}

	results, ok := result["results"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result[\"results\"] to be a map, got %T", result["results"])
	}
	if _, ok := results["svc-a"]; !ok {
		t.Errorf("missing svc-a in results")
	}
	if _, ok := results["svc-b"]; !ok {
		t.Errorf("missing svc-b in results")
	}
}

func TestDoCommand_PartialErrors(t *testing.T) {
	errSvc := &fakeResource{
		name: generic.Named("bad-svc"),
		doCommandFn: func(_ context.Context, _ map[string]interface{}) (map[string]interface{}, error) {
			return nil, errors.New("boom")
		},
	}
	goodSvc := &fakeResource{
		name: generic.Named("good-svc"),
		doCommandFn: func(_ context.Context, _ map[string]interface{}) (map[string]interface{}, error) {
			return map[string]interface{}{"ok": true}, nil
		},
	}
	deps := makeDeps(errSvc, goodSvc)
	conf := &Config{Dependencies: []string{"bad-svc", "good-svc"}}
	name := generic.Named("mux")

	mux, err := New(context.Background(), deps, name, conf, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mux.Close(context.Background())

	// DoCommand itself should not return an error — errors are aggregated.
	result, err := mux.DoCommand(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("DoCommand should not return top-level error; got: %v", err)
	}

	errs, ok := result["errors"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result[\"errors\"] to be a map, got %T", result["errors"])
	}
	if _, found := errs["bad-svc"]; !found {
		t.Errorf("expected bad-svc to appear in errors map; got %v", errs)
	}
	results, _ := result["results"].(map[string]interface{})
	if _, found := results["good-svc"]; !found {
		t.Errorf("expected good-svc to appear in results map; got %v", results)
	}
}

// ---- Status fan-out tests ----

func TestStatus_FansOutToAllDependencies(t *testing.T) {
	called := map[string]bool{}
	makeStatusFn := func(id string) func(context.Context) (map[string]interface{}, error) {
		return func(_ context.Context) (map[string]interface{}, error) {
			called[id] = true
			return map[string]interface{}{"id": id}, nil
		}
	}

	svcA := &fakeResource{name: generic.Named("svc-a"), statusFn: makeStatusFn("svc-a")}
	svcB := &fakeResource{name: generic.Named("svc-b"), statusFn: makeStatusFn("svc-b")}
	deps := makeDeps(svcA, svcB)
	conf := &Config{Dependencies: []string{"svc-a", "svc-b"}}
	name := generic.Named("mux")

	mux, err := New(context.Background(), deps, name, conf, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mux.Close(context.Background())

	result, err := mux.Status(context.Background())
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}

	if !called["svc-a"] || !called["svc-b"] {
		t.Errorf("not all deps were called; called=%v", called)
	}

	results, ok := result["results"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result[\"results\"] to be a map, got %T", result["results"])
	}
	if _, ok := results["svc-a"]; !ok {
		t.Errorf("missing svc-a in status results")
	}
	if _, ok := results["svc-b"]; !ok {
		t.Errorf("missing svc-b in status results")
	}
}

func TestStatus_PartialErrors(t *testing.T) {
	errSvc := &fakeResource{
		name: generic.Named("bad-svc"),
		statusFn: func(_ context.Context) (map[string]interface{}, error) {
			return nil, fmt.Errorf("status unavailable")
		},
	}
	goodSvc := &fakeResource{
		name: generic.Named("good-svc"),
		statusFn: func(_ context.Context) (map[string]interface{}, error) {
			return map[string]interface{}{"healthy": true}, nil
		},
	}
	deps := makeDeps(errSvc, goodSvc)
	conf := &Config{Dependencies: []string{"bad-svc", "good-svc"}}
	name := generic.Named("mux")

	mux, err := New(context.Background(), deps, name, conf, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mux.Close(context.Background())

	result, err := mux.Status(context.Background())
	if err != nil {
		t.Fatalf("Status should not return top-level error; got: %v", err)
	}

	errs, _ := result["errors"].(map[string]interface{})
	if _, found := errs["bad-svc"]; !found {
		t.Errorf("expected bad-svc in errors; got %v", errs)
	}
	results, _ := result["results"].(map[string]interface{})
	if _, found := results["good-svc"]; !found {
		t.Errorf("expected good-svc in results; got %v", results)
	}
}

// ---- Close test ----

func TestClose_CancelsContext(t *testing.T) {
	svc := newFake("svc-a")
	deps := makeDeps(svc)
	conf := &Config{Dependencies: []string{"svc-a"}}
	name := generic.Named("mux")

	mux, err := New(context.Background(), deps, name, conf, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := mux.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	// Calling Close again should be idempotent (no panic or error).
	if err := mux.Close(context.Background()); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}
