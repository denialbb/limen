// Package sandbox confines a model-backed worker or validator to a per-task
// bubblewrap sandbox: a minimal read-only system tree, the task worktree
// read-write, a private /tmp and HOME, no host secrets, and an isolated network
// namespace whose only egress is a host-side broker reached over a unix socket.
//
// Callers describe what runs and how much egress it gets. They never spell out
// bubblewrap flags, and they cannot disable the sandbox: a missing runtime is a
// hard error (Available), not a fallback.
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Trust is the operator's per-invocation judgement about a repository. It can
// only be granted on the command line, never by content in the repository.
type Trust int

const (
	// Untrusted is the default: no package-registry egress.
	Untrusted Trust = iota
	// Trusted additionally allows the explicitly configured package registries.
	Trusted
)

// Provider describes one agent backend's credential and endpoint needs.
type Provider struct {
	// Name is used in errors.
	Name string
	// Hosts are the provider endpoints a worker may reach.
	Hosts []string
	// ExtraHosts are additional CONNECT destinations the CLI needs (for
	// example its own model registry or telemetry endpoint).
	ExtraHosts []string
	// BaseURLEnv names the environment variables a broker-capable CLI reads to
	// find its API endpoint. Empty means the backend cannot be pointed at a
	// broker and authenticates from a mounted credential file instead.
	BaseURLEnv []string
	// CredEnv names the host environment variable holding the real credential.
	// The broker injects it; the sandbox only ever sees a dummy.
	CredEnv string
	// CredHeader is the request header carrying the credential.
	CredHeader string
	// CredScheme prefixes the credential value ("Bearer" or empty).
	CredScheme string
	// SecretMounts are host credential files mounted read-only into the
	// sandbox for backends that cannot be pointed at a broker (agy).
	SecretMounts []Mount
	// DummyEnv are placeholder credentials broker-capable CLIs need to start.
	// The broker replaces them with the host credential.
	DummyEnv []string
}

// Policy is everything the sandbox needs to know about one run.
type Policy struct {
	// Worktree is the only host path the sandbox may write to.
	Worktree string
	// Trust grants package-registry egress when Trusted.
	Trust Trust
	// Registries are the package-registry hosts trusted repositories may reach.
	Registries []string
	// Provider describes the backend's credential and endpoint needs.
	Provider Provider
	// BridgeAddr is the loopback address the in-sandbox bridge listens on.
	// Empty disables networking entirely (validators, tests).
	BridgeAddr string
	// BrokerSocket is the host broker socket bind-mounted into the sandbox.
	BrokerSocket string
	// CallbackSocket is the host callback socket bind-mounted into the sandbox.
	CallbackSocket string
	// LimenBinary is mounted read-only as /usr/local/bin/limen so the worker can
	// call ready-for-review.
	LimenBinary string
	// ExtraMounts are additional host paths mounted into the sandbox (generated
	// CLI configuration for broker-capable backends).
	ExtraMounts []Mount
	// ExtraPath are additional directories prepended to the sandbox PATH. They
	// must also appear in ExtraMounts so the binaries are reachable.
	ExtraPath []string
}

// Mount is one extra bind mount. ReadOnly mounts cannot be written by the task.
type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// sandboxLimenPath is where the limen binary appears inside the sandbox.
const sandboxLimenPath = "/opt/limen/limen"

// sandboxBrokerPath and sandboxCallbackPath are the in-sandbox socket paths.
const (
	sandboxBrokerPath   = "/run/limen/broker.sock"
	sandboxCallbackPath = "/run/limen/callback.sock"
)

// systemReadOnly are the host paths the sandbox sees read-only. /etc is
// enumerated rather than bound whole: binding all of /etc would expose host SSH
// host keys and other machine-local files the task has no business reading.
var systemReadOnly = []string{
	"/usr",
	"/bin",
	"/sbin",
	"/lib",
	"/lib64",
	"/etc/passwd",
	"/etc/group",
	"/etc/nsswitch.conf",
	"/etc/hosts",
	"/etc/resolv.conf",
	"/etc/localtime",
	"/etc/ssl",
	"/etc/ca-certificates",
	"/etc/pki",
	"/etc/gitconfig",
}

// bwrapBinary is a test seam: tests point it at a missing binary to exercise
// the fail-closed path on hosts that do have bubblewrap.
var bwrapBinary = "bwrap"

// Available reports whether this machine can run a sandbox. It fails closed:
// callers must treat any error as "do not run the task".
func Available() error {
	if _, err := exec.LookPath(bwrapBinary); err != nil {
		return errors.New("sandbox: bubblewrap (bwrap) is not installed; install bubblewrap to run untrusted repositories")
	}
	if b, err := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone"); err == nil {
		if strings.TrimSpace(string(b)) == "0" {
			return errors.New("sandbox: unprivileged user namespaces are disabled (kernel.unprivileged_userns_clone=0); enable them or run on a host that allows them")
		}
	}
	return nil
}

// Wrap rewrites cmd so it runs inside the sandbox described by p. The caller
// keeps using *exec.Cmd; the returned error means the task must not run.
func Wrap(cmd *exec.Cmd, p Policy) error {
	if err := Available(); err != nil {
		return err
	}
	if len(cmd.Args) == 0 {
		return errors.New("sandbox: empty command")
	}
	if !filepath.IsAbs(p.Worktree) {
		return fmt.Errorf("sandbox: worktree path %q must be absolute", p.Worktree)
	}
	if info, err := os.Stat(p.Worktree); err != nil || !info.IsDir() {
		return fmt.Errorf("sandbox: worktree %q is not a directory", p.Worktree)
	}
	if p.BridgeAddr != "" && p.BrokerSocket == "" {
		return fmt.Errorf("sandbox: provider %q needs a broker socket", p.Provider.Name)
	}
	if p.BridgeAddr != "" && len(p.Provider.Hosts) == 0 {
		return fmt.Errorf("sandbox: provider %q has no allowlisted hosts; egress would be unbounded", p.Provider.Name)
	}

	bwrap, err := exec.LookPath(bwrapBinary)
	if err != nil {
		return fmt.Errorf("sandbox: locate bwrap: %w", err)
	}
	// exec.Cmd runs Path, not Args[0]: repoint both at bubblewrap.
	cmd.Path = bwrap
	cmd.Args = append(bwrapArgs(p), sandboxCommand(p, cmd.Args)...)
	cmd.Env = sandboxEnv(p)
	return nil
}

// bwrapArgs builds the bubblewrap invocation up to the "--" separator.
func bwrapArgs(p Policy) []string {
	args := []string{
		"bwrap",
		"--die-with-parent",
		"--new-session",
		"--unshare-all",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--dir", "/tmp/home",
		"--bind", p.Worktree, p.Worktree,
		"--chdir", p.Worktree,
		"--setenv", "HOME", "/tmp/home",
		"--setenv", "TMPDIR", "/tmp",
	}
	for _, host := range systemReadOnly {
		args = append(args, "--ro-bind-try", host, host)
	}
	for _, secret := range p.Provider.SecretMounts {
		args = append(args, "--ro-bind", secret.Source, secret.Target)
	}
	for _, m := range p.ExtraMounts {
		args = append(args, "--dir", filepath.Dir(m.Target))
		if m.ReadOnly {
			args = append(args, "--ro-bind", m.Source, m.Target)
		} else {
			args = append(args, "--bind", m.Source, m.Target)
		}
	}
	if p.BrokerSocket != "" {
		args = append(args, "--dir", filepath.Dir(sandboxBrokerPath),
			"--bind", p.BrokerSocket, sandboxBrokerPath)
	}
	if p.CallbackSocket != "" {
		args = append(args, "--dir", filepath.Dir(sandboxCallbackPath),
			"--bind", p.CallbackSocket, sandboxCallbackPath)
	}
	if p.LimenBinary != "" {
		args = append(args, "--dir", "/opt/limen", "--ro-bind", p.LimenBinary, sandboxLimenPath)
	}
	return append(args, "--")
}

// sandboxCommand returns the command bubblewrap runs inside the sandbox. With a
// bridge, the limen binary starts it in the sandbox's network namespace and then
// execs the real command; without one, the command runs directly.
func sandboxCommand(p Policy, argv []string) []string {
	if p.BridgeAddr == "" {
		return argv
	}
	return append([]string{
		sandboxLimenPath, "sandbox-bridge",
		"--socket", sandboxBrokerPath,
		"--listen", p.BridgeAddr,
		"--",
	}, argv...)
}

// sandboxEnv builds the child environment from scratch. Nothing from the host
// environment is inherited: an untrusted repository must not be able to read
// provider credentials, tokens, or paths out of the parent process.
func sandboxEnv(p Policy) []string {
	path := strings.Join(append(append([]string{"/opt/limen"}, p.ExtraPath...),
		"/usr/local/bin", "/usr/local/sbin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"), ":")
	env := []string{
		"PATH=" + path,
		"HOME=/tmp/home",
		"TMPDIR=/tmp",
		"TERM=dumb",
		"LANG=C.UTF-8",
		"CI=1",
	}
	if p.CallbackSocket != "" {
		env = append(env, "LIMEN_IPC_SOCKET="+sandboxCallbackPath)
	}
	if p.BridgeAddr != "" {
		env = append(env, "HTTP_PROXY=http://"+p.BridgeAddr, "HTTPS_PROXY=http://"+p.BridgeAddr)
		for _, name := range p.Provider.BaseURLEnv {
			env = append(env, name+"=http://"+p.BridgeAddr)
		}
		env = append(env, p.Provider.DummyEnv...)
	}
	return env
}
