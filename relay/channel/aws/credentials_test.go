package aws

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAwsCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		want    awsCredentialParts
		wantErr bool
	}{
		{
			name: "bearer api key",
			key:  "sk-token|us-east-1",
			want: awsCredentialParts{apiKey: "sk-token", region: "us-east-1"},
		},
		{
			name: "bearer blank region preserved",
			key:  "sk-token|",
			want: awsCredentialParts{apiKey: "sk-token", region: ""},
		},
		{
			name: "legacy sigv4 with region",
			key:  "AKID|SECRET|us-west-2",
			want: awsCredentialParts{accessKey: "AKID", secretKey: "SECRET", region: "us-west-2"},
		},
		{
			name: "legacy blank region preserved",
			key:  "AKID|SECRET|",
			want: awsCredentialParts{accessKey: "AKID", secretKey: "SECRET", region: ""},
		},
		{
			name: "three fields always region",
			// A region-shaped third field is a region, never a session token.
			key:  "AKID|SECRET|us-west-2",
			want: awsCredentialParts{accessKey: "AKID", secretKey: "SECRET", region: "us-west-2"},
		},
		{
			name: "sts with region",
			key:  "ASID|SECRET|TOKEN|eu-central-1",
			want: awsCredentialParts{accessKey: "ASID", secretKey: "SECRET", sessionToken: "TOKEN", region: "eu-central-1"},
		},
		{
			name: "sts blank region defaults",
			key:  "ASID|SECRET|TOKEN|",
			want: awsCredentialParts{accessKey: "ASID", secretKey: "SECRET", sessionToken: "TOKEN", region: defaultAwsRegion},
		},
		{
			name: "region shaped token",
			// Four fields: the third is always the session token even if it
			// looks like a region.
			key:  "ASID|SECRET|us-west-2|us-east-1",
			want: awsCredentialParts{accessKey: "ASID", secretKey: "SECRET", sessionToken: "us-west-2", region: "us-east-1"},
		},
		{
			name: "whitespace trimmed",
			key:  " ASID | SECRET | TOKEN | eu-west-1 ",
			want: awsCredentialParts{accessKey: "ASID", secretKey: "SECRET", sessionToken: "TOKEN", region: "eu-west-1"},
		},
		{
			name:    "single field",
			key:     "onlyone",
			wantErr: true,
		},
		{
			name:    "empty string",
			key:     "",
			wantErr: true,
		},
		{
			name:    "sigv4 missing secret",
			key:     "AKID||us-west-2",
			wantErr: true,
		},
		{
			name:    "sts missing token",
			key:     "ASID|SECRET||us-east-1",
			wantErr: true,
		},
		{
			name:    "sts missing access key",
			key:     "|SECRET|TOKEN|us-east-1",
			wantErr: true,
		},
		{
			name:    "too many fields",
			key:     "a|b|c|d|e",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseAwsCredentials(tt.key)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// capturingRoundTripper records the last request the SDK signed so the test can
// assert on the produced headers without hitting AWS.
type capturingRoundTripper struct {
	req *http.Request
}

func (c *capturingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.req = req
	// Short-circuit: the test only cares about the signed request, not a real
	// Bedrock response.
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("{}")),
		Request:    req,
	}, nil
}

func TestAwsClientSignsWithSessionToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		creds        awsCredentialParts
		wantToken    string
		wantTokenHdr bool
	}{
		{
			name:         "sts default region",
			creds:        awsCredentialParts{accessKey: "ASID", secretKey: "SECRET", sessionToken: "SESSION", region: defaultAwsRegion},
			wantToken:    "SESSION",
			wantTokenHdr: true,
		},
		{
			name:         "sts explicit region",
			creds:        awsCredentialParts{accessKey: "ASID", secretKey: "SECRET", sessionToken: "SESSION", region: "us-east-1"},
			wantToken:    "SESSION",
			wantTokenHdr: true,
		},
		{
			name:         "static sigv4 no token",
			creds:        awsCredentialParts{accessKey: "AKID", secretKey: "SECRET", region: "us-east-1"},
			wantTokenHdr: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rt := &capturingRoundTripper{}
			client := bedrockruntime.New(bedrockruntime.Options{
				Region:      tt.creds.region,
				Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(tt.creds.accessKey, tt.creds.secretKey, tt.creds.sessionToken)),
				HTTPClient:  &http.Client{Transport: rt},
			})

			_, _ = client.InvokeModel(context.Background(), &bedrockruntime.InvokeModelInput{
				ModelId:     aws.String("anthropic.claude-3-5-sonnet-20240620-v1:0"),
				Accept:      aws.String("application/json"),
				ContentType: aws.String("application/json"),
				Body:        []byte(`{"messages":[]}`),
			})

			require.NotNil(t, rt.req, "SDK did not issue a request")

			authz := rt.req.Header.Get("Authorization")
			require.Contains(t, authz, "/us-east-1/bedrock/aws4_request", "unexpected signing scope")

			securityToken := rt.req.Header.Get("X-Amz-Security-Token")
			if tt.wantTokenHdr {
				assert.Equal(t, tt.wantToken, securityToken)
				assert.Contains(t, authz, "x-amz-security-token", "token must be in SignedHeaders")
			} else {
				assert.Empty(t, securityToken, "static credentials must not send a security token")
				assert.NotContains(t, authz, "x-amz-security-token")
			}
		})
	}
}
