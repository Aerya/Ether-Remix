// Package pseenv lit les réglages PSE : la valeur enregistrée depuis la
// console l'emporte sur la variable d'environnement du même nom. Ajout PSE :
// voir docs/pse/FORK.fr.md.
package pseenv

import (
	"os"
	"sync/atomic"
)

var overrides atomic.Pointer[map[string]string]

// Lookup renvoie la valeur effective d'un réglage et s'il est défini.
func Lookup(name string) (string, bool) {
	if m := overrides.Load(); m != nil {
		if v, ok := (*m)[name]; ok {
			return v, true
		}
	}
	return os.LookupEnv(name)
}

// Get renvoie la valeur effective d'un réglage, ou "".
func Get(name string) string {
	v, _ := Lookup(name)
	return v
}

// Source indique d'où vient la valeur : "console", "env" ou "".
func Source(name string) string {
	if m := overrides.Load(); m != nil {
		if _, ok := (*m)[name]; ok {
			return "console"
		}
	}
	if _, ok := os.LookupEnv(name); ok {
		return "env"
	}
	return ""
}

// Env renvoie la seule valeur de l'environnement.
func Env(name string) (string, bool) { return os.LookupEnv(name) }

// Replace remplace l'ensemble des valeurs enregistrées depuis la console et
// renvoie la fonction qui rétablit les précédentes (tests).
func Replace(values map[string]string) (restore func()) {
	cp := make(map[string]string, len(values))
	for k, v := range values {
		cp[k] = v
	}
	prev := overrides.Swap(&cp)
	return func() { overrides.Store(prev) }
}

// Snapshot renvoie une copie des valeurs enregistrées depuis la console.
func Snapshot() map[string]string {
	out := map[string]string{}
	if m := overrides.Load(); m != nil {
		for k, v := range *m {
			out[k] = v
		}
	}
	return out
}
