// Package stdlib registers Go-backed replacements for common Python stdlib
// modules. Each module is self-registered via py.RegisterModule in its own
// init() function, so importing this package is sufficient to make the modules
// available in every new gpython context.
package stdlib
