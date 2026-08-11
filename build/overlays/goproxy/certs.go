package goproxy

import "crypto/tls"

// GoproxyCa is intentionally empty. CC Switch always supplies its
// installation-scoped CA through TLSConfigFromCA and never uses the insecure
// process-wide example CA shipped by upstream goproxy.
var GoproxyCa tls.Certificate

var tlsClientSkipVerify = &tls.Config{InsecureSkipVerify: true}

var defaultTLSConfig = &tls.Config{InsecureSkipVerify: true}
