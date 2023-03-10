package hivemapper_hdc_acl

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"

	"github.com/streamingfast/solana-go"
)

const AclFileName = "acl.data"

var ErrSignatureRequired = fmt.Errorf("ACL on device requires a signature to be cleared")

type Acl struct {
	Version   string   `json:"version,omitempty"`
	Managers  []string `json:"managers"`
	Drivers   []string `json:"drivers"`
	FleetName string   `json:"fleetName,omitempty"`
}

func NewAclFromFile(sourceFolder string) (*Acl, error) {
	aclFile, err := os.Open(path.Join(sourceFolder, AclFileName))
	if err != nil {
		return nil, fmt.Errorf("opening acl aclFile: %s", err)
	}

	aclData, err := io.ReadAll(aclFile)
	if err != nil {
		return nil, fmt.Errorf("reading acl aclFile: %s", err)
	}

	if len(aclData) == 0 {
		err := clearCorruptedAcl(sourceFolder)
		if err != nil {
			return nil, err
		}
		panic("Found and removed corrupted ACL. Please try locking again.")
	}

	acl, err := NewAclFromData(aclData)
	if err != nil {
		return nil, fmt.Errorf("creating acl: %s", err)
	}

	return acl, nil
}

func NewAclFromData(data []byte) (*Acl, error) {
	acl := &Acl{}

	err := json.Unmarshal(data, acl)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling acl data: %s", err)
	}

	return acl, nil
}

func AclExistOnDevice(sourceFolder string) bool {
	if _, err := os.Stat(path.Join(sourceFolder, AclFileName)); errors.Is(err, os.ErrNotExist) {
		return false
	}
	return true
}

func AclClearFromDevice(aclFolder string, signatureB58 string) error {
	if AclExistOnDevice(aclFolder) {
		acl, err := NewAclFromFile(aclFolder)
		if err != nil {
			return fmt.Errorf("unable to read acl: %w", err)
		}

		if acl.Version != "" && signatureB58 == "" {
			return ErrSignatureRequired
		}

		if signatureB58 != "" {
			signature, err := solana.NewSignatureFromBase58(signatureB58)
			if err != nil {
				return fmt.Errorf("unable to decode signature: %w", err)
			}
			if !acl.ValidateClearSignature(signature) {
				return fmt.Errorf("invalid signature")
			}
		}

		if err := aclClearFromDevice(aclFolder); err != nil {
			return fmt.Errorf("unable to clear acl: %w", err)
		}
	}
	return nil
}

func clearCorruptedAcl(sourceFolder string) error {
	corruptedAcl := path.Join(sourceFolder, AclFileName)
	if _, err := os.Stat(corruptedAcl); err == nil {
		if err := os.Remove(corruptedAcl); err != nil {
			return fmt.Errorf("removing corrupted acl file: %s", err)
		}
	}

	return nil
}

func aclClearFromDevice(sourceFolder string) error {
	aclFile := path.Join(sourceFolder, AclFileName)
	if _, err := os.Stat(aclFile); err == nil {
		if err := os.Remove(aclFile); err != nil {
			return fmt.Errorf("removing acl file: %s", err)
		}
	}

	return nil
}

func (a *Acl) Store(destinationFolder string, signature solana.Signature) error {
	if !a.ValidateStoreSignature(signature) {
		return fmt.Errorf("invalid signature")
	}

	data, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("marshalling acl: %s", err)
	}

	if err := os.MkdirAll(destinationFolder, os.ModePerm); err != nil {
		log.Fatal(err)
	}
	aclFile := path.Join(destinationFolder, AclFileName)

	if len(data) == 0 {
		panic("empty acl")
	}

	f, err := os.OpenFile(aclFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("opening file %w", err)
	}

	_, werr := f.Write(data)

	if err := f.Sync(); err != nil {
		return fmt.Errorf("synching file %w", err)
	}

	if cerr := f.Close(); cerr != nil && werr == nil {
		return fmt.Errorf("closing file %w", cerr)
	}

	if werr != nil {
		return fmt.Errorf("writing file %w", werr)
	}

	return nil
}

func (a *Acl) clearMessageToSign() ([]byte, error) {
	message := fmt.Sprintf("Clearing Access Control List for fleet %s", a.FleetName)

	return []byte(message), nil
}

func (a *Acl) legacyStoreMessageToSign() ([]byte, error) {
	hashableAcl := struct {
		Managers []string `json:"managers"`
		Drivers  []string `json:"drivers"`
	}{
		Managers: a.Managers,
		Drivers:  a.Drivers,
	}
	data, err := json.Marshal(hashableAcl)
	if err != nil {
		return nil, fmt.Errorf("marshalling acl: %s", err)
	}

	return data, nil
}

func (a *Acl) storeMessageToSign() ([]byte, error) {
	hashableAcl := struct {
		FleetName string   `json:"fleetName,omitempty"`
		Managers  []string `json:"managers"`
		Drivers   []string `json:"drivers"`
	}{
		Managers: a.Managers,
		Drivers:  a.Drivers,
	}

	if a.FleetName != "" {
		hashableAcl.FleetName = a.FleetName
	}

	data, err := json.Marshal(hashableAcl)
	if err != nil {
		return nil, fmt.Errorf("marshalling acl: %s", err)
	}

	h := md5.New()
	s := string(data)
	io.WriteString(h, s)

	hash := h.Sum(nil)
	hexHash := hex.EncodeToString(hash)
	message := fmt.Sprintf("Access Control List with %d manager(s) and %d driver(s). Hash: %s", len(a.Managers), len(a.Drivers), hexHash)

	return []byte(message), nil
}

func (a *Acl) ValidateStoreSignature(signature solana.Signature) bool {
	data, err := a.storeMessageToSign()
	if err != nil {
		return false
	}
	valid := a.validateSignature(data, signature)
	if valid {
		return true
	}

	data, err = a.legacyStoreMessageToSign()
	if err != nil {
		return false
	}

	return a.validateSignature(data, signature)
}

func (a *Acl) ValidateClearSignature(signature solana.Signature) bool {
	data, err := a.clearMessageToSign()
	if err != nil {
		return false
	}
	return a.validateSignature(data, signature)
}

func (a *Acl) validateSignature(data []byte, signature solana.Signature) bool {
	for _, managerAddress := range a.Managers {
		pubKey, err := solana.PublicKeyFromBase58(managerAddress)
		if err != nil {
			return false
		}
		if signature.Verify(pubKey, data) {
			return true
		}
	}
	return false
}
