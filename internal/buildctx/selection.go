package buildctx

import "go/build"

// WithBuildContext preserves freshly resolved module authority while changing
// language/platform selection. Callers must not load serialized root paths.
func (c Config) WithBuildContext(selection build.Context) Config {
	return fromBuildContext(selection, c.environment, c.modulesEnabled, c.moduleRoot, c.modulePath)
}
