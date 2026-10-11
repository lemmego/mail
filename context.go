package mail

import "context"

// Context is context.Context. It is aliased so this package's interfaces read
// in terms of their own vocabulary and a caller never has to import context
// just to implement Transport.
type Context = context.Context
