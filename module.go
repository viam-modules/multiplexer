package multiplexer

import (
	"context"
	"fmt"
	"sync"

	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	generic "go.viam.com/rdk/services/generic"
)

var ResourceMultiplexer = resource.NewModel("viam", "multiplexer", "resource-multiplexer")

func init() {
	resource.RegisterService(generic.API, ResourceMultiplexer,
		resource.Registration[resource.Resource, *Config]{
			Constructor: newResourceMux,
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
		required = append(required, name)
	}
	return required, nil, nil
}

type depEntry struct {
	name string
	svc  resource.Resource
}

type resourceMux struct {
	resource.AlwaysRebuild

	name resource.Name

	logger logging.Logger
	cfg    *Config
	deps   []depEntry

	cancelCtx  context.Context
	cancelFunc func()
}

func newResourceMux(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (resource.Resource, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}

	return New(ctx, deps, rawConf.ResourceName(), conf, logger)
}

func New(ctx context.Context, deps resource.Dependencies, name resource.Name, conf *Config, logger logging.Logger) (resource.Resource, error) {
	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	resolved := make([]depEntry, 0, len(conf.Dependencies))
	for _, depName := range conf.Dependencies {
		var res resource.Resource
		for n, r := range deps {
			if n.Name == depName || n.String() == depName {
				res = r
				break
			}
		}
		if res == nil {
			cancelFunc()
			return nil, fmt.Errorf("could not resolve dependency %q", depName)
		}
		resolved = append(resolved, depEntry{name: depName, svc: res})
	}

	return &resourceMux{
		name:       name,
		logger:     logger,
		cfg:        conf,
		deps:       resolved,
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
	}, nil
}

func (s *resourceMux) Name() resource.Name {
	return s.name
}

func (s *resourceMux) fanOut(
	ctx context.Context,
	op string,
	call func(ctx context.Context, svc resource.Resource) (map[string]interface{}, error),
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

func (s *resourceMux) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	return s.fanOut(ctx, "DoCommand", func(ctx context.Context, svc resource.Resource) (map[string]interface{}, error) {
		return svc.DoCommand(ctx, cmd)
	}), nil
}

func (s *resourceMux) Status(ctx context.Context) (map[string]interface{}, error) {
	return s.fanOut(ctx, "Status", func(ctx context.Context, svc resource.Resource) (map[string]interface{}, error) {
		return svc.Status(ctx)
	}), nil
}

func (s *resourceMux) Close(context.Context) error {
	s.cancelFunc()
	return nil
}
