package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denialbb/limen/internal/sandbox"
)

func TestSandboxProviderForBackends(t *testing.T) {
	tests := []struct {
		name     string
		wo       wiringOptions
		wantHost string
		wantErr  string
	}{
		{
			name:     "pi uses the provider's default upstream",
			wo:       wiringOptions{workerBackend: "pi", workerPiProvider: "mistral"},
			wantHost: "api.mistral.ai",
		},
		{
			name:    "pi fails closed without a known upstream",
			wo:      wiringOptions{workerBackend: "pi", workerPiProvider: "unknown-provider"},
			wantErr: "no broker upstream",
		},
		{
			name:     "claude brokers to anthropic",
			wo:       wiringOptions{workerBackend: "claude"},
			wantHost: "api.anthropic.com",
		},
		{
			name:     "opencode brokers to anthropic",
			wo:       wiringOptions{workerBackend: "opencode"},
			wantHost: "api.anthropic.com",
		},
		{
			name:    "unknown backend has no profile",
			wo:      wiringOptions{workerBackend: "nope"},
			wantErr: "no sandbox profile",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := sandboxProviderFor(tc.wo)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got err %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("sandboxProviderFor: %v", err)
			}
			if len(p.Hosts) == 0 || p.Hosts[0] != tc.wantHost {
				t.Fatalf("hosts = %v, want first %q", p.Hosts, tc.wantHost)
			}
			if p.CredEnv == "" || p.CredHeader == "" {
				t.Fatalf("provider %q has no credential brokering: %+v", p.Name, p)
			}
		})
	}
}

func TestSandboxProviderForAgyMountsCredentialsReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	creds := filepath.Join(home, ".gemini", "oauth_creds.json")
	if err := os.MkdirAll(filepath.Dir(creds), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(creds, []byte(`{"access_token":"x"}`), 0600); err != nil {
		t.Fatalf("write creds: %v", err)
	}

	p, err := sandboxProviderFor(wiringOptions{workerBackend: "agy"})
	if err != nil {
		t.Fatalf("sandboxProviderFor: %v", err)
	}
	if len(p.SecretMounts) != 1 {
		t.Fatalf("want one credential mount, got %+v", p.SecretMounts)
	}
	m := p.SecretMounts[0]
	if !m.ReadOnly {
		t.Fatal("agy credentials must be mounted read-only")
	}
	if m.Source != creds || !strings.HasSuffix(m.Target, ".gemini/oauth_creds.json") {
		t.Fatalf("unexpected mount: %+v", m)
	}
	if len(p.BaseURLEnv) != 0 {
		t.Fatal("agy must not be treated as a base-URL backend")
	}
}

func TestSandboxProviderForAgyFailsClosedWithoutLogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := sandboxProviderFor(wiringOptions{workerBackend: "agy"}); err == nil {
		t.Fatal("expected an error when agy has no stored credentials")
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://api.mistral.ai":                "api.mistral.ai",
		"http://127.0.0.1:8080/v1":              "127.0.0.1",
		"https://api.anthropic.com/v1/messages": "api.anthropic.com",
	}
	for raw, want := range cases {
		got, err := hostOf(raw)
		if err != nil {
			t.Fatalf("hostOf(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("hostOf(%q) = %q, want %q", raw, got, want)
		}
	}
	if _, err := hostOf("https://"); err == nil {
		t.Fatal("expected an error for an empty host")
	}
}

func TestTrustOf(t *testing.T) {
	if trustOf(wiringOptions{}) != 0 {
		t.Fatal("repositories must be untrusted by default")
	}
	if trustOf(wiringOptions{trustRepo: true}) != 1 {
		t.Fatal("--trust-repo must select the trusted class")
	}
}

func TestProviderConfigMountsForPi(t *testing.T) {
	dir := t.TempDir()
	mounts, err := providerConfigMounts(dir, wiringOptions{workerBackend: "pi", workerPiProvider: "mistral"}, mustProvider(t, "pi"))
	if err != nil {
		t.Fatalf("providerConfigMounts: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("want one generated config mount, got %+v", mounts)
	}
	data, err := os.ReadFile(mounts[0].Source)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !strings.Contains(string(data), "http://127.0.0.1:4711") {
		t.Fatalf("generated config does not point at the bridge: %s", data)
	}
	if !strings.HasSuffix(mounts[0].Target, "models.json") {
		t.Fatalf("unexpected mount target: %s", mounts[0].Target)
	}
}

func mustProvider(t *testing.T, backend string) sandbox.Provider {
	t.Helper()
	got, err := sandboxProviderFor(wiringOptions{workerBackend: backend, workerPiProvider: "mistral"})
	if err != nil {
		t.Fatalf("sandboxProviderFor(%q): %v", backend, err)
	}
	return got
}
