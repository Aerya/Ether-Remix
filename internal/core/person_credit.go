package core

// PersonCredit is one person's credit on one title, as the catalogue
// stores it: the kind ("cast", "director", "writer", "crew") and the
// person as credited there (name, role, portrait, ids).
type PersonCredit struct {
	Key       RatingKey
	Kind      string
	Person    Person
	Browsable bool
}

// PersonCreditForm is one way a person's credits name them: the name as
// credited and whether that credit carries a portrait.
type PersonCreditForm struct {
	Name     string
	Portrait bool
}

// PersonMetadata holds optional cached person profile facts from the metadata provider.
type PersonMetadata struct {
	KnownFor  string
	Biography string
	Birthday  string
}
