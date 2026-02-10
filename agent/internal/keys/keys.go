package keys

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type StoredKey struct {
	Name       string `json:"name"`
	Address    string `json:"address"`
	PubKeyHex  string `json:"pubkey_hex"`
	PrivKeyHex string `json:"privkey_hex"`
	CreatedAt  string `json:"created_at"`
}

func EnsureKey(path, name string) (StoredKey, bool, error) {
	if key, err := Load(path); err == nil {
		return key, false, nil
	}
	key, err := Generate(name)
	if err != nil {
		return StoredKey{}, false, err
	}
	if err := Save(path, key); err != nil {
		return StoredKey{}, false, err
	}
	return key, true, nil
}

func Generate(name string) (StoredKey, error) {
	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey()
	addr := sdk.AccAddress(pub.Address()).String()

	return StoredKey{
		Name:       name,
		Address:    addr,
		PubKeyHex:  hex.EncodeToString(pub.Bytes()),
		PrivKeyHex: hex.EncodeToString(priv.Bytes()),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func Save(path string, key StoredKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	bz, err := json.MarshalIndent(key, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, bz, 0o600)
}

func Load(path string) (StoredKey, error) {
	bz, err := os.ReadFile(path)
	if err != nil {
		return StoredKey{}, err
	}
	var key StoredKey
	if err := json.Unmarshal(bz, &key); err != nil {
		return StoredKey{}, err
	}
	if key.Address == "" {
		return StoredKey{}, fmt.Errorf("invalid key file: missing address")
	}
	return key, nil
}

func DefaultUserKeyPath(base string) string {
	return filepath.Join(base, "user.json")
}

func DefaultAgentKeyPath(base string) string {
	return filepath.Join(base, "agent.json")
}
