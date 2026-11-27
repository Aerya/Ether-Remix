package frontend

// PSEThemeCatalog est une rangée de bibliothèque Remix publiée comme
// catalogue par un client qui n'a pas de notion de bibliothèque (Stremio).
// ID est l'identifiant de la collection ; Type vaut « movie » ou « series ».
type PSEThemeCatalog struct {
	ID   string
	Name string
	Type string
}
