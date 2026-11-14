package ftp

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
)

// Entry est un élément d'un dossier distant.
type Entry struct {
	Path  string
	Size  int64
	MTime time.Time
	Dir   bool
}

// Remote est un serveur FTP ou SFTP. Ses méthodes sont sûres en parallèle.
type Remote interface {
	// ReadDir liste un dossier (sans « . » ni « .. »).
	ReadDir(ctx context.Context, dir string) ([]Entry, error)
	// Stat décrit un fichier.
	Stat(ctx context.Context, p string) (Entry, error)
	// Open lit un fichier dans l'ordre à partir de off. Fermer le lecteur
	// libère la connexion (SFTP : rien à libérer).
	Open(ctx context.Context, p string, off int64) (io.ReadCloser, error)
	// Close coupe toutes les connexions.
	Close() error
}

// ErrNotFound : le fichier n'est plus sur le serveur.
var ErrNotFound = errors.New("fichier absent du serveur")

// errClosed : la source a été arrêtée.
var errClosed = errors.New("connexion fermée")

// NewRemote ouvre (paresseusement) le serveur décrit par s.
func NewRemote(s Settings) (Remote, error) {
	switch s.Protocol {
	case ProtoSFTP:
		return newSFTP(s)
	case ProtoFTP, ProtoFTPS, ProtoFTPSImpl:
		return newFTP(s), nil
	}
	return nil, errors.New("protocole inconnu")
}

// scrub retire d'un message d'erreur ce qui ressemblerait à un secret.
func scrub(err error, s Settings) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, secret := range []string{s.Password, s.Passphrase} {
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "••••")
		}
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}
