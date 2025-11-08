package config

import "errors"

var ErrMissingCaCert = errors.New("missing ca cert")
var ErrMissingCaKey = errors.New("missing ca key")
var ErrMissingCert = errors.New("missing cert")
var ErrMissingKey = errors.New("missing key")
var ErrUnknownProtocol = errors.New("unknown protocol")
var ErrConfigIsNil = errors.New("config is nil")
