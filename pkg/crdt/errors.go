package crdt

import "fmt"

var ErrInvalidDeltaType = fmt.Errorf("invalid delta type")
var ErrCRDTTypeMismatch = fmt.Errorf("CRDT type mismatch")
var ErrDeltaTypeMismatch = fmt.Errorf("delta type mismatch")
