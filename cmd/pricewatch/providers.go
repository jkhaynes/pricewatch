package main

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/jkhaynes/pricewatch/internal/pipeline"
	"github.com/jkhaynes/pricewatch/internal/resolve"
	"github.com/jkhaynes/pricewatch/internal/source"
	"github.com/jkhaynes/pricewatch/internal/source/pokewallet"
)

// provider is everything the commands need from one price source.
type provider struct {
	name    string
	catalog resolve.Catalog
	prices  pipeline.PriceSource
}

type providerOpts struct {
	BaseURL string // empty: the provider's default
	Timeout time.Duration
	Quota   source.Quota
	Limits  *source.Limits // nil: the provider's published limits
}

// providers is the swap point. Adding a source is one entry here plus its package.
var providers = map[string]func(providerOpts) (provider, error){
	pokewallet.Name: func(o providerOpts) (provider, error) {
		key := os.Getenv("POKEWALLET_API_KEY")
		if key == "" {
			return provider{}, errors.New("POKEWALLET_API_KEY is not set")
		}
		cfg := pokewallet.Config(cmp.Or(o.BaseURL, pokewallet.DefaultBaseURL), key, o.Quota, o.Timeout)
		if o.Limits != nil {
			cfg.Limits = *o.Limits
		}
		p := pokewallet.New(source.New(cfg))
		return provider{name: pokewallet.Name, catalog: p, prices: p}, nil
	},
}

func newProvider(name string, o providerOpts) (provider, error) {
	mk, ok := providers[name]
	if !ok {
		return provider{}, fmt.Errorf("unknown source %q (known: %v)", name, slices.Sorted(maps.Keys(providers)))
	}
	return mk(o)
}
