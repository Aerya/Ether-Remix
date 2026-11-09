package plex

import (
	"regexp"
	"strings"
)

// Les titres de sections sont rédigés en anglais par le moteur de
// recommandations, partagé par tous les frontends. La traduction se fait ici,
// au dernier moment du rendu Plex : identifiants, classement et caches ne
// dépendent ainsi jamais de la langue. Un titre inconnu reste inchangé.
var hubTitlesFR = map[string]string{
	"Acclaimed & Unseen":                     "Acclamés et jamais vus",
	"Airing Today":                           "Diffusés aujourd'hui",
	"Comfort Rewatches":                      "À revoir sans modération",
	"Complete Series to Binge":               "Séries complètes à dévorer",
	"Continue Watching":                      "Reprendre",
	"Currently Airing for You":               "En cours de diffusion pour vous",
	"Date Night":                             "Soirée à deux",
	"Family Night":                           "Soirée en famille",
	"Fans of What You Watch Also Loved":      "Les fans de vos programmes ont aussi aimé",
	"Favorites":                              "Favoris",
	"Films Loved by Fans of What You Watch":  "Films aimés par les fans de vos programmes",
	"Finish What You Started":                "À terminer",
	"Hidden Gems for You":                    "Pépites cachées pour vous",
	"Limited Series for You":                 "Mini-séries pour vous",
	"More Like This":                         "Dans le même genre",
	"New & Noteworthy":                       "Nouveautés à ne pas manquer",
	"New Releases":                           "Nouvelles sorties",
	"New Seasons of Your Shows":              "Nouvelles saisons de vos séries",
	"Now Playing":                            "Actuellement au cinéma",
	"On The Air":                             "En cours de diffusion",
	"Ongoing Series for You":                 "Séries en cours pour vous",
	"Popular":                                "Populaires",
	"Popular Movies":                         "Films populaires",
	"Popular Shows":                          "Séries populaires",
	"Recent Playlists":                       "Listes de lecture récentes",
	"Recently Added":                         "Ajouts récents",
	"Recently Watched Episodes":              "Épisodes vus récemment",
	"Recently Watched Movies":                "Films vus récemment",
	"Recommended For You":                    "Recommandés pour vous",
	"Rediscover":                             "À redécouvrir",
	"Series Loved by Fans of What You Watch": "Séries aimées par les fans de vos programmes",
	"Series That Stick the Landing":          "Séries à la fin réussie",
	"Short on Time":                          "Quand on a peu de temps",
	"Something Different":                    "Autre chose",
	"Standalone & Anthology Episodes":        "Épisodes indépendants et anthologies",
	"Start Something New":                    "À commencer",
	"Top 10 Today":                           "Top 10 du jour",
	"Top 10 for You":                         "Top 10 pour vous",
	"Top Rated":                              "Les mieux notés",
	"Top Rated Movies":                       "Films les mieux notés",
	"Top Rated Shows":                        "Séries les mieux notées",
	"Trakt Recommendations":                  "Recommandations Trakt",
	"Trending Movies This Week":              "Films tendance de la semaine",
	"Trending Movies Today":                  "Films tendance du jour",
	"Trending Now":                           "Tendances du moment",
	"Trending Shows This Week":               "Séries tendance de la semaine",
	"Trending Shows Today":                   "Séries tendance du jour",
	"Trending With Fans of What You Watch":   "Tendances chez les fans de vos programmes",
	"Trending in Your Taste":                 "Tendances selon vos goûts",
	"Under 100 Minutes":                      "Moins de 100 minutes",
	"Upcoming Movies":                        "Films à venir",
	"Waiting for a stream":                   "En attente d'une source",
	"Now available":                          "Maintenant disponibles",
	"Your Taste Clusters":                    "Vos univers",
	"Your Trakt Watchlist":                   "Votre liste Trakt",
}

var hubGenresFR = map[string]string{
	"Action": "Action", "Adventures": "Aventure", "Animation": "Animation", "Comedies": "Comédies", "Comedy": "Comédie",
	"Crime": "Policier", "Documentaries": "Documentaires", "Documentary": "Documentaire", "Dramas": "Drames", "Drama": "Drame",
	"Family": "Famille", "Family Favourites": "Famille", "Fantasy": "Fantastique", "History": "Histoire", "Horror": "Horreur",
	"Mysteries": "Mystère", "Mystery": "Mystère", "Romances": "Romance", "Romance": "Romance", "Sci-Fi": "Science-fiction",
	"Science Fiction": "Science-fiction", "Thrillers": "Thrillers", "Thriller": "Thriller", "War": "Guerre", "Western": "Western",
}

func hubGenreFR(value string) string {
	for _, suffix := range []struct{ en, fr string }{{" Movies", " (films)"}, {" Films", " (films)"}, {" Shows", " (séries)"}, {" Series", " (séries)"}} {
		if base, ok := strings.CutSuffix(value, suffix.en); ok {
			return hubGenreFR(base) + suffix.fr
		}
	}
	if translated := hubGenresFR[value]; translated != "" {
		return translated
	}
	return value
}

var hubPatternsFR = []struct {
	re     *regexp.Regexp
	render func([]string) string
}{
	{regexp.MustCompile(`^Recently Added in (.+)$`), func(m []string) string { return "Ajouts récents dans " + m[1] }},
	{regexp.MustCompile(`^Because You Watched (.+)$`), func(m []string) string { return "Parce que vous avez regardé " + m[1] }},
	{regexp.MustCompile(`^More Written by (.+)$`), func(m []string) string { return "Écrit aussi par " + m[1] }},
	{regexp.MustCompile(`^More with (.+)$`), func(m []string) string { return "Avec aussi " + m[1] }},
	{regexp.MustCompile(`^More from (.+)$`), func(m []string) string { return "Plus de " + m[1] }},
	{regexp.MustCompile(`^Created by (.+)$`), func(m []string) string { return "Créé par " + m[1] }},
	{regexp.MustCompile(`^Shot by (.+)$`), func(m []string) string { return "Image de " + m[1] }},
	{regexp.MustCompile(`^Scored by (.+)$`), func(m []string) string { return "Musique de " + m[1] }},
	{regexp.MustCompile(`^Popular on (.+)$`), func(m []string) string { return "Populaires sur " + m[1] }},
	{regexp.MustCompile(`^Top 10 on (.+) Today$`), func(m []string) string { return "Top 10 du jour sur " + m[1] }},
	{regexp.MustCompile(`^Top-Rated (.+)$`), func(m []string) string { return "Les mieux notés : " + hubGenreFR(m[1]) }},
	{regexp.MustCompile(`^Acclaimed (.+)$`), func(m []string) string { return "Acclamés : " + hubGenreFR(m[1]) }},
	{regexp.MustCompile(`^New This Week in (.+) & (.+)$`), func(m []string) string {
		return "Nouveautés de la semaine : " + hubGenreFR(m[1]) + " et " + hubGenreFR(m[2])
	}},
	{regexp.MustCompile(`^New This Week in (.+)$`), func(m []string) string { return "Nouveautés de la semaine : " + hubGenreFR(m[1]) }},
	{regexp.MustCompile(`^Best of the (\d{4})s for You$`), func(m []string) string { return "Le meilleur des années " + m[1] + " pour vous" }},
	{regexp.MustCompile(`^Where (.+) Meets (.+)$`), func(m []string) string { return "Entre " + m[1] + " et " + m[2] }},
	{regexp.MustCompile(`^Finish the (.+) Films$`), func(m []string) string { return "À terminer : les films " + m[1] }},
	{regexp.MustCompile(`^Finish (.+)$`), func(m []string) string { return "À terminer : " + m[1] }},
}

// localizedHubTitle traduit un titre de section pour un client francophone.
func localizedHubTitle(language, title string) string {
	if primaryLang(language) != "fr" {
		return title
	}
	if translated := hubTitlesFR[title]; translated != "" {
		return translated
	}
	for _, pattern := range hubPatternsFR {
		if match := pattern.re.FindStringSubmatch(title); match != nil {
			return pattern.render(match)
		}
	}
	return title
}
