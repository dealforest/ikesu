package config

func (c Config) ScreenPrompts() bool {
	if c.Decisions.ScreenPrompts != nil {
		return *c.Decisions.ScreenPrompts
	}
	return !c.isV2()
}
