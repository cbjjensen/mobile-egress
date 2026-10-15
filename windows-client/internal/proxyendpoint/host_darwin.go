//go:build darwin

package proxyendpoint

const Host = "127.0.0.1"

// Refract rejects literal localhost proxy entries. This is only the address
// exported to applications; listeners always bind to the numeric Host above.
const httpProxyHost = "localtest.me"
