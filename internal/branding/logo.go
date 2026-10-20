package branding

import _ "embed"

// Logo is shared by the public brand endpoint and inline transactional emails.
//
//go:embed logo.png
var Logo []byte

const EmailLogoCID = "atlantic-express-logo"
