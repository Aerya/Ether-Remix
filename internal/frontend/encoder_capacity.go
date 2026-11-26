package frontend

import (
	"errors"
	"fmt"

	"github.com/braedonsaunders/ether/internal/transcode"
	"golang.org/x/text/language"
)

// In playbackErrorLocaleMatcher order. Keep viewer names off client responses.
var videoEncoderCapacityText = [...]string{
	"The server is already converting video for %d other viewers. Try again in a few minutes, or pick a lower quality.",
	"Der Server konvertiert bereits Videos für %d andere Zuschauer. Versuche es in ein paar Minuten erneut oder wähle eine niedrigere Qualität.",
	"El servidor ya está convirtiendo vídeo para otros espectadores (%d). Vuelve a intentarlo en unos minutos o elige una calidad inferior.",
	"Le serveur convertit déjà des vidéos pour d’autres spectateurs (%d). Réessaie dans quelques minutes ou choisis une qualité inférieure.",
	"Il server sta già convertendo video per altri spettatori (%d). Riprova tra qualche minuto o scegli una qualità inferiore.",
	"サーバーはすでに他の視聴者（%d 人）向けに動画を変換しています。数分後に再試行するか、画質を下げてください。",
	"서버가 이미 다른 시청자 %d명을 위해 동영상을 변환 중입니다. 몇 분 후 다시 시도하거나 화질을 낮추세요.",
	"De server zet al video om voor %d andere kijkers. Probeer het over een paar minuten opnieuw of kies een lagere kwaliteit.",
	"Serveren konverterer allerede video for %d andre seere. Prøv igjen om noen minutter, eller velg lavere kvalitet.",
	"Serwer konwertuje już wideo dla innych widzów (%d). Spróbuj ponownie za kilka minut lub wybierz niższą jakość.",
	"O servidor já está convertendo vídeo para outros espectadores (%d). Tente novamente em alguns minutos ou escolha uma qualidade menor.",
	"Сервер уже преобразует видео для других зрителей (%d). Попробуйте через несколько минут или выберите более низкое качество.",
	"Servern konverterar redan video för %d andra tittare. Försök igen om några minuter eller välj lägre kvalitet.",
	"Sunucu şu anda başka izleyiciler (%d kişi) için video dönüştürüyor. Birkaç dakika sonra tekrar deneyin veya daha düşük görüntü kalitesi seçin.",
	"Сервер уже перетворює відео для інших глядачів (%d). Спробуйте за кілька хвилин або виберіть нижчу якість.",
	"服务器正在为其他 %d 位观众转换视频。请几分钟后重试，或选择较低画质。",
	"伺服器正在為其他 %d 位觀眾轉換影片。請幾分鐘後再試，或選擇較低畫質。",
	"Serveren konverterer allerede video for %d andre seere. Prøv igen om et par minutter, eller vælg en lavere kvalitet.",
	"Palvelin muuntaa jo videota muille katsojille (%d). Yritä uudelleen muutaman minuutin päästä tai valitse heikompi kuvanlaatu.",
	"Server už převádí video pro jiné diváky (%d). Zkuste to znovu za několik minut nebo vyberte nižší kvalitu.",
}

// VideoEncoderCapacityMessage localizes an exhausted local video budget.
func VideoEncoderCapacityMessage(err error, languages ...string) string {
	var capacity *transcode.EncoderCapacityError
	if !errors.As(err, &capacity) {
		return ""
	}
	_, locale := language.MatchStrings(playbackErrorLocaleMatcher, languages...)
	if locale == 0 && capacity.Running == 1 {
		return "The server is already converting video for 1 other viewer. Try again in a few minutes, or pick a lower quality."
	}
	return fmt.Sprintf(videoEncoderCapacityText[locale], capacity.Running)
}
