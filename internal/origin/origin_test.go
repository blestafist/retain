package origin

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "domain", value: "https://app.example.test"},
		{name: "localhost with explicit port", value: "http://localhost:5173"},
		{name: "IPv4", value: "http://127.0.0.1"},
		{name: "IPv6", value: "https://[2001:db8::1]:8443"},
		{name: "bracketed domain is not IPv6", value: "https://[example.com]", wantErr: true},
		{name: "bracketed IPv4 is not IPv6", value: "https://[127.0.0.1]", wantErr: true},
		{name: "invalid IPv6 literal", value: "https://[2001:db8::zz]", wantErr: true},
		{name: "IPv6 zone identifier", value: "https://[fe80::1%25eth0]", wantErr: true},
		{name: "unbracketed IPv6 literal", value: "https://2001:db8::1", wantErr: true},
		{name: "underscore in hostname", value: "https://app_name.example.test", wantErr: true},
		{name: "empty DNS label", value: "https://app..example.test", wantErr: true},
		{name: "leading hyphen", value: "https://-app.example.test", wantErr: true},
		{name: "trailing DNS dot", value: "https://app.example.test.", wantErr: true},
		{name: "invalid numeric address", value: "https://999.999.999.999", wantErr: true},
		{name: "empty port", value: "https://app.example.test:", wantErr: true},
		{name: "port out of range", value: "https://app.example.test:65536", wantErr: true},
		{name: "path is not an origin", value: "https://app.example.test/", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr %t", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestDefaultPortNormalization(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		origin     string
		host       string
		wantMatch  bool
	}{
		{
			name:       "HTTPS default port is equivalent",
			configured: "https://Example.test",
			origin:     "https://example.test:443",
			host:       "EXAMPLE.TEST:443",
			wantMatch:  true,
		},
		{
			name:       "HTTP default port is equivalent",
			configured: "http://localhost:80",
			origin:     "http://LOCALHOST",
			host:       "localhost",
			wantMatch:  true,
		},
		{
			name:       "non-default port differs",
			configured: "http://localhost:5173",
			origin:     "http://localhost",
			host:       "localhost:5173",
			wantMatch:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configured, err := Parse(tt.configured)
			if err != nil {
				t.Fatalf("Parse(configured) error = %v", err)
			}
			if got := configured.Matches(tt.origin) && configured.MatchesHost(tt.host); got != tt.wantMatch {
				t.Errorf("origin and Host match = %t, want %t", got, tt.wantMatch)
			}
		})
	}
}
