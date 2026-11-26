package admin

import (
	"encoding/base64"
	"net/url"

	qrcode "github.com/skip2/go-qrcode"
)

// pseStremioFiles ajoute au résultat le second lien Stremio d'un profil :
// même jeton, addon « fichiers seuls » (aucun catalogue), avec son QR code.
// Voir docs/pse/FORK.fr.md.
func pseStremioFiles(result map[string]string, u *url.URL, basePath, secret string) {
	f := *u
	f.Path = basePath + "/" + secret + "/fichiers/manifest.json"
	result["filesUrl"] = f.String()
	if png, err := qrcode.Encode(f.String(), qrcode.Medium, 256); err == nil {
		result["filesQr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
}
