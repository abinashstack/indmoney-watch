package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/abinashstack/indmoney-watch/internal/config"
	"github.com/abinashstack/indmoney-watch/internal/oauth"
)

// Location describes where tokens are stored, for user-facing messages.
const Location = "DPAPI-encrypted file in the indmoney-watch config folder (tokens.dpapi)"

// dpapiEntropy is mixed into the DPAPI key derivation so that other programs
// calling CryptUnprotectData on the file without it can't decrypt it. It is
// not a secret — it only stops casual, generic DPAPI dumps.
var dpapiEntropy = []byte("indmoney-watch/tokens/v1")

// tokensFile is a var so tests can point at a scratch location.
var tokensFile = func() (string, error) {
	d, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "tokens.dpapi"), nil
}

// SaveTokens encrypts the token bundle with DPAPI (CryptProtectData) and
// writes it atomically. DPAPI binds the ciphertext to the current Windows
// user's credentials: other users, and the same files copied to another
// machine, can't decrypt it. Credential Manager was not used because its
// 2.5 KB blob limit is too small for JWT access + refresh tokens.
func SaveTokens(t *oauth.Tokens) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	enc, err := dpapiProtect(b)
	if err != nil {
		return fmt.Errorf("dpapi protect: %w", err)
	}
	p, err := tokensFile()
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(p, enc, 0o600)
}

func LoadTokens() (*oauth.Tokens, error) {
	p, err := tokensFile()
	if err != nil {
		return nil, err
	}
	enc, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("no stored tokens")
		}
		return nil, err
	}
	b, err := dpapiUnprotect(enc)
	if err != nil {
		return nil, fmt.Errorf("dpapi unprotect: %w", err)
	}
	var t oauth.Tokens
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("decode tokens: %w", err)
	}
	return &t, nil
}

func blob(b []byte) *windows.DataBlob {
	if len(b) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func dpapiProtect(plain []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptProtectData(blob(plain), nil, blob(dpapiEntropy), 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(&out), nil
}

func dpapiUnprotect(enc []byte) ([]byte, error) {
	return dpapiUnprotectWithEntropy(enc, dpapiEntropy)
}

func dpapiUnprotectWithEntropy(enc, entropy []byte) ([]byte, error) {
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(blob(enc), nil, blob(entropy), 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return takeBlob(&out), nil
}

// takeBlob copies a DPAPI-allocated output buffer into Go memory and frees it.
func takeBlob(b *windows.DataBlob) []byte {
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(b.Data)))
	return append([]byte(nil), unsafe.Slice(b.Data, b.Size)...)
}
