// Package alko ajoute à Ether-Remix la source « Alkodiques (API StreamFusion) » :
// le catalogue des fichiers AllDebrid vérifiés d'un StreamFusion Reborn, lu par
// son API HTTP en lecture seule (docs/pse/ALKODIQUES.fr.md).
//
// Remix tient un miroir local (table pse_alko_files) synchronisé selon
// l'algorithme du lecteur d'SFR : lecture complète paginée, puis journal des
// modifications. La recherche à la lecture ne fait aucun appel réseau : elle lit
// ce miroir. Le lien source (qui contient un jeton AllDebrid) est chiffré au
// repos et ne figure jamais dans un journal, une erreur ou la console.
package alko
