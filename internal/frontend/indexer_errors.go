package frontend

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
	"golang.org/x/text/language"
)

type IndexersPausedError = core.IndexersPausedError

const IndexersPausedCode = "indexer_paused"

var playbackErrorLocaleMatcher = language.NewMatcher([]language.Tag{
	language.English, language.German, language.Spanish, language.French, language.Italian,
	language.Japanese, language.Korean, language.Dutch, language.Norwegian, language.Polish,
	language.MustParse("pt-BR"), language.Russian, language.Swedish, language.Turkish,
	language.Ukrainian, language.SimplifiedChinese, language.TraditionalChinese,
	language.Danish, language.Finnish, language.Czech,
})

var indexersPausedText = [...]struct{ noSearch, paused, unnamed string }{
	{"no indexer could be searched: %s", "%s paused (timing out)", "indexer"},
	{"kein Indexer konnte durchsucht werden: %s", "%s pausiert (Zeitüberschreitungen)", "Indexer"},
	{"no se pudo consultar ningún indexador: %s", "%s en pausa (tiempo de espera agotado)", "indexador"},
	{"aucun indexeur n’a pu être interrogé : %s", "%s en pause (délais d’attente dépassés)", "indexeur"},
	{"impossibile interrogare gli indicizzatori: %s", "%s in pausa (timeout)", "indicizzatore"},
	{"インデクサーを検索できませんでした：%s", "%s は一時停止中（タイムアウト）", "インデクサー"},
	{"인덱서를 검색할 수 없습니다: %s", "%s 일시 중지됨 (시간 초과)", "인덱서"},
	{"geen indexer kon worden doorzocht: %s", "%s gepauzeerd (time-outs)", "indexer"},
	{"ingen indekserere kunne søkes i: %s", "%s satt på pause (tidsavbrudd)", "indekserer"},
	{"nie udało się przeszukać żadnego indeksatora: %s", "%s wstrzymany (przekroczono czas oczekiwania)", "indeksator"},
	{"nenhum indexador pôde ser consultado: %s", "%s pausado (tempo limite excedido)", "indexador"},
	{"не удалось выполнить поиск ни в одном индексаторе: %s", "%s приостановлен (превышено время ожидания)", "индексатор"},
	{"ingen indexerare kunde sökas igenom: %s", "%s pausad (tidsgränsen överskrids)", "indexerare"},
	{"hiçbir indeksleyicide arama yapılamadı: %s", "%s duraklatıldı (zaman aşımı)", "indeksleyici"},
	{"не вдалося виконати пошук у жодному індексаторі: %s", "%s призупинено (перевищено час очікування)", "індексатор"},
	{"无法搜索任何索引器：%s", "%s 已暂停（请求超时）", "索引器"},
	{"無法搜尋任何索引器：%s", "%s 已暫停（要求逾時）", "索引器"},
	{"ingen indekser blev søgt: %s", "%s sat på pause (tidsgrænsen overskrides)", "indekser"},
	{"haku ei onnistunut yhdestäkään indeksoijasta: %s", "%s tauolla (aikakatkaisuja)", "indeksoija"},
	{"v žádném indexeru se nepodařilo vyhledat: %s", "%s pozastaven (vypršel časový limit)", "indexer"},
}

// IndexersPausedMessage renders only the structured pause diagnostic. It
// deliberately ignores all transport error text and connection details.
func IndexersPausedMessage(err error, languages ...string) string {
	var paused *core.IndexersPausedError
	if !errors.As(err, &paused) {
		return ""
	}
	_, locale := language.MatchStrings(playbackErrorLocaleMatcher, languages...)
	t := indexersPausedText[locale]
	reasons := make([]string, 0, len(paused.Names))
	for _, name := range paused.Names {
		name = strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, name))
		if name == "" {
			name = t.unnamed
		}
		reasons = append(reasons, fmt.Sprintf(t.paused, name))
	}
	return fmt.Sprintf(t.noSearch, strings.Join(reasons, ", "))
}

// WriteIndexersPaused serves the diagnostic for protocols with plain-text
// playback errors. Structured decision endpoints use IndexersPausedMessage.
func WriteIndexersPaused(w http.ResponseWriter, err error, languages ...string) bool {
	msg := IndexersPausedMessage(err, languages...)
	if msg == "" {
		return false
	}
	w.Header().Set("X-Ether-Media-Error", IndexersPausedCode)
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, msg, http.StatusServiceUnavailable)
	return true
}

// WriteAppConnectionError reports admission failures before media headers are sent.
func WriteAppConnectionError(w http.ResponseWriter, r *http.Request, err error) {
	if WriteIndexersPaused(w, err, r.Header.Get("Accept-Language")) {
		return
	}
	var reqLang *RequiredAudioLanguageError
	if errors.As(err, &reqLang) {
		http.Error(w, reqLang.Error(), http.StatusUnprocessableEntity)
		return
	}
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	status := http.StatusServiceUnavailable
	switch {
	case errors.Is(err, ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, ErrStreamLimit):
		status = http.StatusTooManyRequests
	}
	http.Error(w, "Playback unavailable", status)
}
