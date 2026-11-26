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
	pseStremioOptionQRs(result, u, basePath+"/"+secret, "qr")
	pseStremioOptionQRs(result, u, basePath+"/"+secret+"/fichiers", "filesQr")
}

// pseStremioOptionQRs ajoute le QR code du lien avec les quotas par défaut de
// la console (4K 2, 1080p 3, 720p 1), clé « <prefixe>:<format> » : le secret
// n'étant montré qu'une fois, la console ne peut pas le redemander, et d'autres
// quotas n'ont pas de QR code (le lien se copie).
func pseStremioOptionQRs(result map[string]string, u *url.URL, linkPath, prefix string) {
	for _, format := range []string{"compact", "detaille"} {
		l := *u
		l.Path = linkPath + "/o/" + format + "-2160.2-1080.3-720.1/manifest.json"
		if png, err := qrcode.Encode(l.String(), qrcode.Medium, 256); err == nil {
			result[prefix+":"+format] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}
}
