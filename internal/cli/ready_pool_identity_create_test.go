package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type readyPoolIdentityValidatorFunc func(CoordinatorLease, ReadyPoolIdentityCreateExpected) error

func (fn readyPoolIdentityValidatorFunc) ValidateReadyPoolIdentityCreateLease(lease CoordinatorLease, expected ReadyPoolIdentityCreateExpected) error {
	return fn(lease, expected)
}

func TestValidateReadyPoolIdentityProviderLeaseRoutesCapability(t *testing.T) {
	want := errors.New("provider rejected lease")
	called := false
	err := validateReadyPoolIdentityProviderLease(
		readyPoolIdentityValidatorFunc(func(CoordinatorLease, ReadyPoolIdentityCreateExpected) error {
			called = true
			return want
		}),
		CoordinatorLease{},
		ReadyPoolIdentityCreateExpected{},
	)
	if !called || !errors.Is(err, want) {
		t.Fatalf("called=%t error=%v", called, err)
	}
	if err := validateReadyPoolIdentityProviderLease(struct{}{}, CoordinatorLease{Provider: "example"}, ReadyPoolIdentityCreateExpected{}); err == nil || !strings.Contains(err.Error(), "does not support verified") {
		t.Fatalf("unsupported capability error=%v", err)
	}
}

func TestReadyPoolIdentityCreateRejectsProviderWithoutValidationCapability(t *testing.T) {
	clearConfigEnv(t)
	root := t.TempDir()
	isolateRunTestUserDirs(t, root)
	const leaseID = "cbx_identity_unsupported"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/leases/"+leaseID {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"lease": CoordinatorLease{
			ID:           leaseID,
			Provider:     runEnvProfileTestProvider{}.Name(),
			TargetOS:     targetLinux,
			Architecture: ArchitectureAMD64,
			SSHHostKey:   "ssh-ed25519 AAAAauthoritative",
		}})
	}))
	t.Cleanup(server.Close)
	configPath := filepath.Join(root, ".crabbox.yaml")
	if err := os.WriteFile(configPath, []byte("coordinator: "+server.URL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRABBOX_CONFIG", configPath)
	output := filepath.Join(root, "identity.json")
	digest := "sha256:" + strings.Repeat("a", 64)
	err := (App{Stdout: os.Stdout, Stderr: os.Stderr}).readyPoolIdentityCreate(t.Context(), []string{
		"--id", leaseID,
		"--repo", "example-org/example",
		"--ref", "main",
		"--commit", strings.Repeat("b", 40),
		"--fingerprint", "setup-v1",
		"--expected-image", "image-1",
		"--expected-type", "standard-1",
		"--expected-architecture", ArchitectureAMD64,
		"--expected-profile", "linux-builder",
		"--expected-recipe-digest", digest,
		"--cache-abi-digest", "sha256:" + strings.Repeat("c", 64),
		"--output", output,
	})
	if err == nil || !strings.Contains(err.Error(), "does not support verified ready-pool identity creation") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(output); !os.IsNotExist(statErr) {
		t.Fatalf("unsupported provider wrote identity output: %v", statErr)
	}
}

func TestValidateReadyPoolIdentityCreateLeaseRequiresExactAWSShape(t *testing.T) {
	expected := ReadyPoolIdentityCreateExpected{
		ImageID:      "ami-0123456789abcdef0",
		ServerType:   "m7i.large",
		Architecture: ArchitectureAMD64,
		Profile:      "linux-builder",
		RecipeDigest: "sha256:" + strings.Repeat("a", 64),
	}
	valid := CoordinatorLease{
		Provider:     "aws",
		TargetOS:     targetLinux,
		Architecture: "x86_64",
		ServerType:   "m7i.large",
		SSHHostKey:   "ssh-ed25519 AAAAauthoritative",
		Image:        &CoordinatorLeaseImage{ID: "ami-0123456789abcdef0"},
	}
	if err := validateReadyPoolIdentityCreateLease(valid, expected); err != nil {
		t.Fatalf("valid lease rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*CoordinatorLease)
		want   string
	}{
		{name: "target", mutate: func(lease *CoordinatorLease) { lease.TargetOS = targetWindows }, want: "native Linux"},
		{name: "host key", mutate: func(lease *CoordinatorLease) { lease.SSHHostKey = "" }, want: "authoritative coordinator SSH host key"},
		{name: "architecture", mutate: func(lease *CoordinatorLease) { lease.Architecture = ArchitectureARM64 }, want: "expected architecture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lease := valid
			image := *valid.Image
			lease.Image = &image
			tc.mutate(&lease)
			err := validateReadyPoolIdentityCreateLease(lease, expected)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestWriteReadyPoolIdentityAtomicCreatesPrivateCanonicalFile(t *testing.T) {
	output := filepath.Join(t.TempDir(), "identity.json")
	identity := CoordinatorReadyPoolIdentityV1{
		Schema:          readyPoolIdentitySchemaV1,
		Profile:         "linux-builder",
		RecipeDigest:    "sha256:" + strings.Repeat("a", 64),
		InventoryDigest: "sha256:" + strings.Repeat("b", 64),
		ImageID:         "ami-0123456789abcdef0",
		Architecture:    ArchitectureAMD64,
		SeedDigest:      "sha256:" + strings.Repeat("c", 64),
		CacheABIDigest:  "sha256:" + strings.Repeat("d", 64),
	}
	if err := writeReadyPoolIdentityAtomic(output, identity); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode=%o, want 600", got)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var decoded CoordinatorReadyPoolIdentityV1
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != identity {
		t.Fatalf("decoded=%#v, want %#v", decoded, identity)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("identity lacks trailing newline: %q", data)
	}
	if err := writeReadyPoolIdentityAtomic(output, CoordinatorReadyPoolIdentityV1{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite error=%v", err)
	}
}

func TestPoolIdentityCreateAppearsInKongHelp(t *testing.T) {
	var stdout strings.Builder
	err := (App{Stdout: &stdout, Stderr: &stdout}).Run(t.Context(), []string{"pool", "identity", "--help"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "create") {
		t.Fatalf("pool identity help=%q", stdout.String())
	}
}
