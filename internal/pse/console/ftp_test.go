package console

import (
	"strings"
	"testing"
)

func TestFTPFieldsAndValidation(t *testing.T) {
	var found bool
	for _, f := range Features {
		if f.ID != "ftp" {
			continue
		}
		found = true
		for _, fd := range f.Fields {
			switch fd.Key {
			case "PSE_FTP":
				if fd.Default != "off" {
					t.Fatal("la source FTP doit être désactivée par défaut")
				}
			case "PSE_FTP_MODE":
				if fd.Default != "stream" {
					t.Fatal("le mode par défaut ne doit rien écrire sur le disque")
				}
			case "PSE_FTP_PASSWORD", "PSE_FTP_KEY_PASSPHRASE":
				if fd.Kind != KindSecret {
					t.Fatalf("%s doit être un champ secret", fd.Key)
				}
			}
		}
	}
	if !found {
		t.Fatal("réglages FTP absents")
	}
	for _, c := range []struct {
		key, v string
		ok     bool
	}{
		{"PSE_FTP_HOST", "seedbox.exemple.net", true},
		{"PSE_FTP_HOST", "sftp://seedbox", false},
		{"PSE_FTP_HOST", "seedbox:22", false},
		{"PSE_FTP_KEY_FILE", "/config/ssh/id_ed25519", true},
		{"PSE_FTP_KEY_FILE", "id_ed25519", false},
		{"PSE_FTP_KEY_FILE", "/config/../etc/shadow", false},
		{"PSE_FTP_DOWNLOAD_DIR", "/", false},
		{"PSE_FTP_HOST_KEY", "SHA256:" + strings.Repeat("A", 43), true},
		{"PSE_FTP_HOST_KEY", "ssh-ed25519 AAAA", false},
		{"PSE_FTP_ROOTS", "/films ; series", true},
		{"PSE_FTP_ROOTS", "/films/../..", false},
		{"PSE_FTP_PORT", "70000", false},
	} {
		out, handled, err := validateFTP(c.key, c.v)
		if !handled || (err == nil) != c.ok {
			t.Errorf("%s=%q : %q %v", c.key, c.v, out, err)
		}
	}
	if out, _, _ := validateFTP("PSE_FTP_ROOTS", "/films ; series"); out != "/films ; /series" {
		t.Fatal(out)
	}
}

// Un mot de passe n'est jamais renvoyé à la page, même en partie, et le
// masque renvoyé tel quel garde la valeur enregistrée.
func TestSecretFieldIsNeverDisplayed(t *testing.T) {
	fd := Field{Key: "PSE_FTP_PASSWORD", Kind: KindSecret}
	const pw = "Un mot de passe : très secret !"
	if d := display(fd, pw); d != mask || strings.Contains(d, pw[:4]) {
		t.Fatalf("affiché : %q", d)
	}
	if display(fd, "") != "" {
		t.Fatal("champ vide affiché comme rempli")
	}
	if restore(fd, mask, pw) != pw || restore(fd, "nouveau", pw) != "nouveau" {
		t.Fatal("restauration")
	}
	if _, err := validate(fd, pw, nil); err != nil {
		t.Fatalf("mot de passe avec espaces et ponctuation refusé : %v", err)
	}
	for _, bad := range []string{"a" + mask, "ligne\nsuivante", strings.Repeat("x", 600)} {
		if _, err := validate(fd, bad, nil); err == nil {
			t.Fatalf("%q accepté", bad)
		}
	}
}

func TestFTPSecretSavePreservesWhitespace(t *testing.T) {
	h, _, stored := newHandler(t)
	code, out := do(t, h, "PUT", "/", `{"values":{"PSE_FTP_PASSWORD":" secret avec espaces "}}`)
	if code != 200 {
		t.Fatalf("PUT %d %v", code, out)
	}
	if stored()["PSE_FTP_PASSWORD"] != " secret avec espaces " {
		t.Fatal("mot de passe modifié")
	}
	if field(t, out, "PSE_FTP_PASSWORD")["value"] != mask {
		t.Fatal("secret exposé")
	}
}
