//go:build setup_payload

package setup

import _ "embed"

// The guarded release build creates this archive from already signed artifacts,
// embeds it, then signs the entire setup executable with the established identity.
//
//go:embed payload.zip
var embeddedPayload []byte
