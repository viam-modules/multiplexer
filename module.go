package multiplexer

import (
	"context"
	"fmt"
	"sync"

	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	generic "go.viam.com/rdk/services/generic"
)

var GenericServiceMultiplexer = resource.NewModel("viam", "multiplexer", "generic-service-multiplexer")

func init() {
	resource.RegisterService(generic.API, GenericServiceMultiplexer,
		resource.Registration[resource.Resource, *Config]{
			Constructor: newMultiplexerGenericServiceMultiplexer,
		},
	)
}

type Config struct {
	Dependencies []string `json:"dependencies"`
}

func (cfg *Config) Validate(path string) ([]string, []string, error) {
	if len(cfg.Dependencies) == 0 {
		return nil, nil, fmt.Errorf("%s: at least one dependency is required", path)
	}
	required := make([]string, 0, len(cfg.Dependencies))
	for i, name := range cfg.Dependencies {
		if name == "" {
			return nil, nil, fmt.Errorf("%s.dependencies[%d]: empty dependency name", path, i)
		}
		required = append(required, generic.Named(name).String())
	}
	return required, nil, nil
}

type depEntry struct {
	name string
	svc  generic.Service
}

type multiplexerGenericServiceMultiplexer struct {
	resource.AlwaysRebuild

	name resource.Name

	logger logging.Logger
	cfg    *Config
	deps   []depEntry

	cancelCtx  context.Context
	cancelFunc func()
}

func newMultiplexerGenericServiceMultiplexer(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (resource.Resource, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}

	return NewGenericServiceMultiplexer(ctx, deps, rawConf.ResourceName(), conf, logger)
}

func NewGenericServiceMultiplexer(ctx context.Context, deps resource.Dependencies, name resource.Name, conf *Config, logger logging.Logger) (resource.Resource, error) {
	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	resolved := make([]depEntry, 0, len(conf.Dependencies))
	for _, depName := range conf.Dependencies {
		res, err := deps.Lookup(generic.Named(depName))
		if err != nil {
			cancelFunc()
			return nil, fmt.Errorf("could not resolve dependency %q: %w", depName, err)
		}
		svc, ok := res.(generic.Service)
		if !ok {
			cancelFunc()
			return nil, fmt.Errorf("dependency %q is not a generic.Service (got %T)", depName, res)
		}
		resolved = append(resolved, depEntry{name: depName, svc: svc})
	}

	return &multiplexerGenericServiceMultiplexer{
		name:       name,
		logger:     logger,
		cfg:        conf,
		deps:       resolved,
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
	}, nil
}

func (s *multiplexerGenericServiceMultiplexer) Name() resource.Name {
	return s.name
}

func (s *multiplexerGenericServiceMultiplexer) fanOut(
	ctx context.Context,
	op string,
	call func(ctx context.Context, svc generic.Service) (map[string]interface{}, error),
) map[string]interface{} {
	type out struct {
		name string
		res  map[string]interface{}
		err  error
	}

	ch := make(chan out, len(s.deps))
	var wg sync.WaitGroup
	for _, d := range s.deps {
		wg.Add(1)
		go func(d depEntry) {
			defer wg.Done()
			res, err := call(ctx, d.svc)
			ch <- out{name: d.name, res: res, err: err}
		}(d)
	}
	wg.Wait()
	close(ch)

	results := map[string]interface{}{}
	errs := map[string]interface{}{}
	for o := range ch {
		if o.err != nil {
			s.logger.Warnf("%s on dep %q failed: %v", op, o.name, o.err)
			errs[o.name] = o.err.Error()
			continue
		}
		results[o.name] = o.res
	}
	return map[string]interface{}{"results": results, "errors": errs}
}

func (s *multiplexerGenericServiceMultiplexer) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	return s.fanOut(ctx, "DoCommand", func(ctx context.Context, svc generic.Service) (map[string]interface{}, error) {
		return svc.DoCommand(ctx, cmd)
	}), nil
}

func (s *multiplexerGenericServiceMultiplexer) Status(ctx context.Context) (map[string]interface{}, error) {
	return s.fanOut(ctx, "Status", func(ctx context.Context, svc generic.Service) (map[string]interface{}, error) {
		return svc.Status(ctx)
	}), nil
}

func (s *multiplexerGenericServiceMultiplexer) Close(context.Context) error {
	s.cancelFunc()
	return nil
}
