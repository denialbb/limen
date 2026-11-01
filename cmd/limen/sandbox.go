package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/denialbb/limen/internal/remote"
	"github.com/denialbb/limen/internal/sandbox"
)

// bridgeAddr is the loopback address the in-sandbox bridge listens on. Each
// sandbox has its own network namespace, so a fixed port cannot collide.
const bridgeAddr = "127.0.0.1:4711"

// defaultUpstreams maps a pi provider to the API host the broker forwards to.
var defaultUpstreams = map[string]string{
	"mistral":   "https://api.mistral.ai",
	"openai":    "https://api.openai.com",
	"anthropic": "https://api.anthropic.com",
}

// sandboxWiring owns the host-side sandbox resources for one run: the egress
// broker, the callback mediator, and the generated CLI configuration. It is
// built once per run and closed when the run ends.
type sandboxWiring struct {
	dir       string
	broker    *sandbox.Broker
	callback  *sandbox.CallbackServer
	worker    remote.SandboxFunc
	validator remote.SandboxFunc
}

// close releases the broker, the callback server, and the temporary directory.
func (s *sandboxWiring) close() {
	if s == nil {
		return
	}
	if s.broker != nil {
		_ = s.broker.Close()
	}
	if s.callback != nil {
		_ = s.callback.Close()
	}
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
}

// workerSandboxable reports whether the worker backend launches a model CLI.
// The mock and cli backends are local test doubles with no subprocess, so they
// are not sandboxed.
func workerSandboxable(backend string) bool {
	switch backend {
	case "pi", "claude", "opencode", "agy":
		return true
	default:
		return false
	}
}

// validatorSandboxable reports whether the validator backend launches a model
// CLI. The shell and mock validators run locally.
func validatorSandboxable(backend string) bool {
	switch backend {
	case "claude", "opencode", "agy":
		return true
	default:
		return false
	}
}

// toolPathMounts exposes the operator's tool directories to the sandbox
// read-only, so the agent finds the same CLIs and toolchains it would on the
// host. Only directories already on PATH are exposed; nothing else from $HOME.
func toolPathMounts() (mounts []sandbox.Mount, path []string) {
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || seen[dir] || strings.HasPrefix(dir, "/usr") || strings.HasPrefix(dir, "/bin") || strings.HasPrefix(dir, "/sbin") || strings.HasPrefix(dir, "/lib") {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		seen[dir] = true
		mounts = append(mounts, sandbox.Mount{Source: dir, Target: dir, ReadOnly: true})
		path = append(path, dir)
	}
	return mounts, path
}

// newSandboxWiring starts the host-side sandbox services for a run and returns
// the wrappers the remote backends use. Any error means the task must not run:
// sandboxing is never skipped as a fallback.
func newSandboxWiring(wo wiringOptions, store sandbox.CallbackStore) (*sandboxWiring, error) {
	if err := sandbox.Available(); err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "limen-sandbox-")
	if err != nil {
		return nil, fmt.Errorf("sandbox: temp dir: %w", err)
	}
	w := &sandboxWiring{dir: dir}

	// The operator's tool directories are exposed read-only so the agent finds
	// the same CLIs it would on the host. Nothing else from $HOME is mounted.
	toolMounts, toolPath := toolPathMounts()
	policy := sandbox.Policy{
		Trust:       trustOf(wo),
		Registries:  wo.registries,
		ExtraMounts: toolMounts,
		ExtraPath:   toolPath,
	}

	if workerSandboxable(wo.workerBackend) {
		if err := w.startWorkerSandbox(wo, store, &policy); err != nil {
			w.close()
			return nil, err
		}
	}

	if validatorSandboxable(wo.validatorBackend) {
		// Validators run repository tests: no bridge, no broker, no callback, and
		// no limen binary. They report through the stdout sentinel instead.
		validatorPolicy := policy
		w.validator = func(cmd *exec.Cmd, worktree string) error {
			p := validatorPolicy
			p.Worktree = worktree
			return sandbox.Wrap(cmd, p)
		}
	}

	return w, nil
}

// startWorkerSandbox starts the broker and callback services and installs the
// worker wrapper. Only the worker gets egress and a callback channel.
func (s *sandboxWiring) startWorkerSandbox(wo wiringOptions, store sandbox.CallbackStore, policy *sandbox.Policy) error {
	provider, err := sandboxProviderFor(wo)
	if err != nil {
		return err
	}

	brokerSocket := filepath.Join(s.dir, "broker.sock")
	s.broker, err = sandbox.NewBroker(brokerSocket, provider, wo.registries, wo.trustRepo)
	if err != nil {
		return err
	}
	go func() {
		if err := s.broker.Serve(); err != nil {
			log.Printf("sandbox broker: %v", err)
		}
	}()

	callbackSocket := filepath.Join(s.dir, "callback.sock")
	s.callback = sandbox.NewCallbackServer(callbackSocket, store)
	go func() {
		if err := s.callback.Serve(); err != nil {
			log.Printf("sandbox callback: %v", err)
		}
	}()

	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("sandbox: locate limen binary: %w", err)
	}

	configMounts, err := providerConfigMounts(s.dir, wo, provider)
	if err != nil {
		return err
	}

	policy.Provider = provider
	policy.BridgeAddr = bridgeAddr
	policy.BrokerSocket = brokerSocket
	policy.CallbackSocket = callbackSocket
	policy.LimenBinary = selfPath
	policy.ExtraMounts = append(policy.ExtraMounts, configMounts...)

	workerPolicy := *policy
	s.worker = func(cmd *exec.Cmd, worktree string) error {
		p := workerPolicy
		p.Worktree = worktree
		return sandbox.Wrap(cmd, p)
	}
	return nil
}

// piProviderName is the effective pi provider, mirroring remote's default.
func piProviderName(wo wiringOptions) string {
	if wo.workerPiProvider != "" {
		return wo.workerPiProvider
	}
	return "mistral"
}

// trustOf maps the operator's flag onto the sandbox trust class.
func trustOf(wo wiringOptions) sandbox.Trust {
	if wo.trustRepo {
		return sandbox.Trusted
	}
	return sandbox.Untrusted
}

// sandboxProviderFor describes the selected worker backend's credential and
// egress needs. A backend with no safe description is an error, not a bypass.
func sandboxProviderFor(wo wiringOptions) (sandbox.Provider, error) {
	switch wo.workerBackend {
	case "pi":
		provider := wo.workerPiProvider
		if provider == "" {
			provider = "mistral" // mirrors remote.defaultOptions
		}
		upstream := wo.brokerUpstream
		if upstream == "" {
			upstream = defaultUpstreams[provider]
		}
		if upstream == "" {
			return sandbox.Provider{}, fmt.Errorf(
				"sandbox: no broker upstream known for pi provider %q; pass --broker-upstream", provider)
		}
		host, err := hostOf(upstream)
		if err != nil {
			return sandbox.Provider{}, err
		}
		keyEnv := strings.ToUpper(provider) + "_API_KEY"
		return sandbox.Provider{
			Name:       "pi",
			Hosts:      []string{host},
			CredEnv:    keyEnv,
			CredHeader: "authorization",
			CredScheme: "Bearer",
			DummyEnv:   []string{keyEnv + "=limen-sandbox-dummy"},
		}, nil
	case "claude":
		return sandbox.Provider{
			Name:       "claude",
			Hosts:      []string{"api.anthropic.com"},
			BaseURLEnv: []string{"ANTHROPIC_BASE_URL"},
			CredEnv:    "ANTHROPIC_API_KEY",
			CredHeader: "x-api-key",
			DummyEnv:   []string{"ANTHROPIC_API_KEY=limen-sandbox-dummy"},
		}, nil
	case "opencode":
		return sandbox.Provider{
			Name:       "opencode",
			Hosts:      []string{"api.anthropic.com"},
			ExtraHosts: []string{"opencode.ai"},
			BaseURLEnv: []string{"ANTHROPIC_BASE_URL"},
			CredEnv:    "ANTHROPIC_API_KEY",
			CredHeader: "x-api-key",
			DummyEnv:   []string{"ANTHROPIC_API_KEY=limen-sandbox-dummy"},
		}, nil
	case "agy":
		home, err := os.UserHomeDir()
		if err != nil {
			return sandbox.Provider{}, fmt.Errorf("sandbox: locate agy credentials: %w", err)
		}
		creds := filepath.Join(home, ".gemini", "oauth_creds.json")
		if _, err := os.Stat(creds); err != nil {
			return sandbox.Provider{}, fmt.Errorf(
				"sandbox: agy needs %s for its OAuth login; run agy once outside limen to sign in", creds)
		}
		return sandbox.Provider{
			Name: "agy",
			Hosts: []string{
				"daily-cloudcode-pa.googleapis.com",
				"www.googleapis.com",
				"oauth2.googleapis.com",
				"antigravity-unleash.goog",
			},
			SecretMounts: []sandbox.Mount{{
				Source:   creds,
				Target:   "/tmp/home/.gemini/oauth_creds.json",
				ReadOnly: true,
			}},
		}, nil
	default:
		return sandbox.Provider{}, fmt.Errorf("sandbox: backend %q has no sandbox profile", wo.workerBackend)
	}
}

// hostOf extracts the hostname from a broker upstream URL.
func hostOf(raw string) (string, error) {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	host := strings.SplitN(trimmed, "/", 2)[0]
	host = strings.SplitN(host, ":", 2)[0]
	if host == "" {
		return "", fmt.Errorf("sandbox: invalid broker upstream %q", raw)
	}
	return host, nil
}

// providerConfigMounts generates the per-backend configuration a broker-capable
// CLI needs to find the in-sandbox bridge. pi reads its base URL from
// models.json; the others read an environment variable and need nothing here.
func providerConfigMounts(dir string, wo wiringOptions, provider sandbox.Provider) ([]sandbox.Mount, error) {
	if provider.Name != "pi" {
		return nil, nil
	}
	config := fmt.Sprintf(
		`{"providers":{%q:{"baseUrl":"http://%s"}}}`, piProviderName(wo), bridgeAddr)
	source := filepath.Join(dir, "models.json")
	if err := os.WriteFile(source, []byte(config), 0600); err != nil {
		return nil, fmt.Errorf("sandbox: write pi models.json: %w", err)
	}
	return []sandbox.Mount{{
		Source:   source,
		Target:   "/tmp/home/.pi/agent/models.json",
		ReadOnly: true,
	}}, nil
}

// runSandboxBridgeCmd starts the in-sandbox bridge and then replaces the process
// with the real command. It runs inside the sandbox's network namespace: the
// bridge is the only route out, and it reaches the host broker over a unix
// socket the sandbox cannot use for anything else.
func runSandboxBridgeCmd() {
	flags := flag.NewFlagSet("sandbox-bridge", flag.ExitOnError)
	socket := flags.String("socket", "", "Host broker socket path")
	listen := flags.String("listen", "", "Loopback address to listen on")
	if err := flags.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}
	argv := flags.Args()
	if *socket == "" || *listen == "" || len(argv) == 0 {
		fmt.Fprintf(os.Stderr, "usage: limen sandbox-bridge --socket <path> --listen <addr> -- <command> [args...]\n")
		os.Exit(2)
	}

	go func() {
		if err := sandbox.ServeBridge(*socket, *listen); err != nil {
			fmt.Fprintf(os.Stderr, "limen sandbox-bridge: %v\n", err)
			os.Exit(1)
		}
	}()
	if err := waitForBridge(*listen); err != nil {
		fmt.Fprintf(os.Stderr, "limen sandbox-bridge: %v\n", err)
		os.Exit(1)
	}

	// Run the command as a child rather than exec'ing it: the bridge lives in
	// this process, and exec would take it down with the old image.
	child := exec.Command(argv[0], argv[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.Env = os.Environ()
	if err := child.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "limen sandbox-bridge: %v\n", err)
		os.Exit(1)
	}
}

// waitForBridge blocks until the bridge accepts connections, so the command it
// launches never races the listener.
func waitForBridge(addr string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			return conn.Close()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("bridge did not start on %s: %w", addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
