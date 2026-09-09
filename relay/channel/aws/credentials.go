package aws

import (
	"strings"

	"github.com/pkg/errors"
)

// defaultAwsRegion is used only when a four-part STS credential omits its
// trailing region segment (i.e. "AK|SK|SessionToken|").
const defaultAwsRegion = "us-east-1"

// awsCredentialParts is the parsed form of a channel's AWS credential string.
//
// The credential string packs several AWS auth shapes into a single "|"
// delimited field. The segment count alone selects the shape (no content
// sniffing):
//
//	APIKey|Region                     -> Bearer token (apiKey set)
//	AK|SK|Region                      -> static SigV4 (region may be empty)
//	AK|SK|SessionToken|Region         -> STS temporary credentials
//
// For SigV4/STS shapes apiKey is empty; callers dispatch on apiKey != "".
type awsCredentialParts struct {
	apiKey       string
	accessKey    string
	secretKey    string
	sessionToken string
	region       string
}

// parseAwsCredentials parses a channel AWS credential string. Each segment is
// trimmed before dispatch. The segment count is authoritative: a three-segment
// value is always AK|SK|Region and a four-segment value is always
// AK|SK|SessionToken|Region, even when a segment happens to look like the other
// shape (e.g. a region-shaped session token).
func parseAwsCredentials(key string) (awsCredentialParts, error) {
	rawParts := strings.Split(key, "|")
	parts := make([]string, len(rawParts))
	for i, p := range rawParts {
		parts[i] = strings.TrimSpace(p)
	}

	switch len(parts) {
	case 2:
		// Bearer mode: region is taken as-is and not validated for emptiness,
		// preserving the pre-STS behavior.
		return awsCredentialParts{apiKey: parts[0], region: parts[1]}, nil
	case 3:
		if parts[0] == "" || parts[1] == "" {
			return awsCredentialParts{}, errAwsCredentialFormat()
		}
		// Legacy static SigV4: an empty region is preserved rather than
		// defaulted, so existing channels keep their exact behavior.
		return awsCredentialParts{accessKey: parts[0], secretKey: parts[1], region: parts[2]}, nil
	case 4:
		if parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return awsCredentialParts{}, errAwsCredentialFormat()
		}
		region := parts[3]
		if region == "" {
			region = defaultAwsRegion
		}
		return awsCredentialParts{
			accessKey:    parts[0],
			secretKey:    parts[1],
			sessionToken: parts[2],
			region:       region,
		}, nil
	default:
		return awsCredentialParts{}, errAwsCredentialFormat()
	}
}

func errAwsCredentialFormat() error {
	return errors.New("invalid aws credentials: expected APIKey|region, AK|SK|region, or AK|SK|SessionToken|region (STS region may be empty)")
}
