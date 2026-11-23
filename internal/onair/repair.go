package onair

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/braedonsaunders/ether/internal/onair/scene"
)

// Roles is the complete set of playout roles every full AI-created style covers.
// Keep in sync with ROLES in web/src/pages/livetv/onair/studio-model.ts.
var Roles = []string{
	"ident",
	"upnext",
	"tonight",
	"break",
	"back",
	"offair",
	"welcome",
	"lowerthird",
	"bug",
}

// BundledFonts lists the embedded OFL typefaces shipped with On Air.
var BundledFonts = []string{
	"inter",
	"playfairdisplay",
	"specialelite",
	"fraunces",
	"bungee",
	"fredoka",
	"orbitron",
	"vt323",
	"spacegrotesk",
	"ibmplexmono",
	"archivoblack",
	"notosans",
}

// MissingRoles returns every role in Roles that lacks an enabled, non-empty scene.
func MissingRoles(l Look) []string {
	covered := map[string]bool{}
	for _, sc := range l.Scenes {
		if sc.Enabled && len(sc.Document.Layers) > 0 {
			covered[sc.Role] = true
		}
	}
	var missing []string
	for _, r := range Roles {
		if !covered[r] {
			missing = append(missing, r)
		}
	}
	return missing
}

// ParseLookDraft repairs common model mistakes in raw Look JSON (unsupported
// layer types such as "line", missing/duplicate IDs, out-of-range durations,
// unordered keyframes, low-contrast palettes, unsupported slot/words syntax,
// and unaligned centred headlines) before strict scene decoding.
func ParseLookDraft(data []byte) (Look, error) {
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(data)))
	if err := dec.Decode(&raw); err != nil || raw == nil {
		var zero Look
		if err != nil {
			return zero, err
		}
		return zero, fmt.Errorf("style must be a JSON object")
	}
	repairLookMap(raw)
	repaired, err := json.Marshal(raw)
	if err != nil {
		return Look{}, err
	}
	var out Look
	if err := json.Unmarshal(repaired, &out); err != nil {
		return Look{}, err
	}
	out.emptyCollections()
	return out, nil
}

// ParseSceneDraft repairs common model mistakes in a single StyleScene JSON
// payload before running strict scene decoding.
func ParseSceneDraft(data []byte) (StyleScene, error) {
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(data)))
	if err := dec.Decode(&raw); err != nil || raw == nil {
		if err != nil {
			return StyleScene{}, err
		}
		return StyleScene{}, fmt.Errorf("scene must be a JSON object")
	}
	repairSceneMap(raw, nil, 1, map[string]bool{}, nil)
	repaired, err := json.Marshal(raw)
	if err != nil {
		return StyleScene{}, err
	}
	var out StyleScene
	if err := json.Unmarshal(repaired, &out); err != nil {
		return StyleScene{}, err
	}
	if out.Sounds == nil {
		out.Sounds = []SceneSound{}
	}
	return out, nil
}

// RepairLook normalizes an in-memory Look through the same draft repair pass.
func RepairLook(l Look) (Look, error) {
	raw, err := json.Marshal(l)
	if err != nil {
		return Look{}, err
	}
	return ParseLookDraft(raw)
}

func repairLookMap(raw map[string]any) {
	if s, _ := raw["name"].(string); strings.TrimSpace(s) == "" {
		raw["name"] = "Channel style"
	}
	if s, _ := raw["template"].(string); strings.TrimSpace(s) == "" {
		raw["template"] = "clean"
	}
	brand, _ := raw["brand"].(map[string]any)
	if brand == nil {
		brand = map[string]any{}
	}
	repairBrandMap(brand)
	raw["brand"] = brand

	seenCtrl := map[string]bool{}
	if ctrls, ok := raw["controls"].([]any); ok {
		outCtrls := make([]any, 0, len(ctrls))
		for i, item := range ctrls {
			cm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if repairControlMap(cm, i+1, seenCtrl) {
				outCtrls = append(outCtrls, cm)
			}
		}
		raw["controls"] = outCtrls
	} else if raw["controls"] == nil {
		raw["controls"] = []any{}
	}

	roles := map[string]float64{}
	if rm, ok := raw["roles"].(map[string]any); ok {
		for k, v := range rm {
			role := normalizeRole(k)
			if role == "" {
				continue
			}
			dur := 8.0
			if cm, ok := v.(map[string]any); ok {
				dur = snapDuration(numOr(cm["duration"], 8))
			} else {
				dur = snapDuration(numOr(v, 8))
			}
			roles[role] = dur
		}
	}

	seenSceneIDs := map[string]bool{}
	if scenesRaw, ok := raw["scenes"].([]any); ok {
		outScenes := make([]any, 0, len(scenesRaw))
		for i, item := range scenesRaw {
			sm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			repairSceneMap(sm, roles, i+1, seenSceneIDs, seenCtrl)
			outScenes = append(outScenes, sm)
		}
		raw["scenes"] = outScenes
	} else {
		raw["scenes"] = []any{}
	}

	rolesOut := map[string]any{}
	for role, dur := range roles {
		rolesOut[role] = map[string]any{"duration": dur}
	}
	raw["roles"] = rolesOut

	if raw["channelIds"] == nil {
		raw["channelIds"] = []any{}
	}
}

func repairBrandMap(brand map[string]any) {
	pal, _ := brand["palette"].(map[string]any)
	if pal == nil {
		pal = map[string]any{}
	}
	bg := normalizeHex6(strOr(pal["background"], "#0b1320"), "#0b1320")
	fg := normalizeHex6(strOr(pal["text"], "#f2f5fa"), "#f2f5fa")
	accent := normalizeHex6(strOr(pal["accent"], "#78b9e8"), "#78b9e8")
	glow := normalizeHex6(strOr(pal["glow"], "#17254a"), "#17254a")
	fg = ensureContrastHex(bg, fg)
	pal["background"] = bg
	pal["text"] = fg
	pal["accent"] = accent
	pal["glow"] = glow
	brand["palette"] = pal

	typ, _ := brand["type"].(map[string]any)
	if typ == nil {
		typ = map[string]any{}
	}
	typ["display"] = normalizeBrandFont(strOr(typ["display"], "inter"), "inter")
	typ["body"] = normalizeBrandFont(strOr(typ["body"], "inter"), "inter")
	brand["type"] = typ

	energy := numOr(brand["energy"], 0.5)
	if math.IsNaN(energy) || math.IsInf(energy, 0) {
		energy = 0.5
	} else if energy > 1 && energy <= 100 {
		energy = energy / 100
	}
	brand["energy"] = max(0, min(1, energy))

	if strings.TrimSpace(strOr(brand["language"], "")) == "" {
		brand["language"] = "en"
	}
	if texRaw, ok := brand["textures"].([]any); ok {
		var textures []string
		seen := map[string]bool{}
		for _, item := range texRaw {
			t := strings.ToLower(strings.TrimSpace(fmt.Sprint(item)))
			if strings.Contains(" grain flicker scanlines vignette chroma ", " "+t+" ") && !seen[t] {
				seen[t] = true
				textures = append(textures, t)
			}
		}
		if textures == nil {
			textures = []string{}
		}
		brand["textures"] = textures
	} else {
		brand["textures"] = []string{}
	}
}

func normalizeBrandFont(f, fallback string) string {
	trimmed := strings.TrimSpace(f)
	if len(trimmed) == 64 && isHexString(trimmed) {
		return strings.ToLower(trimmed)
	}
	norm := normalizeFont(trimmed)
	for _, known := range BundledFonts {
		if norm == known {
			return known
		}
	}
	return fallback
}

func repairControlMap(cm map[string]any, idx int, seen map[string]bool) bool {
	id := slugID(strOr(cm["id"], ""))
	if id == "" {
		id = slugID(strOr(cm["label"], ""))
	}
	if id == "" {
		id = fmt.Sprintf("control-%d", idx)
	}
	id = uniqueID(id, seen)
	cm["id"] = id
	if strings.TrimSpace(strOr(cm["label"], "")) == "" {
		cm["label"] = humanizeID(id)
	}
	kind := strings.ToLower(strings.TrimSpace(strOr(cm["kind"], "")))
	switch kind {
	case "number":
		v := numOr(cm["value"], 0)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			v = 0
		}
		if lo, hasMin := optFloat(cm["min"]); hasMin {
			cm["min"] = lo
			if v < lo {
				v = lo
			}
		} else {
			delete(cm, "min")
		}
		if hi, hasMax := optFloat(cm["max"]); hasMax {
			cm["max"] = hi
			if v > hi {
				v = hi
			}
		} else {
			delete(cm, "max")
		}
		if step, hasStep := optFloat(cm["step"]); hasStep && step > 0 {
			cm["step"] = step
		} else {
			delete(cm, "step")
		}
		cm["value"] = v
	case "toggle":
		b, ok := cm["value"].(bool)
		if !ok {
			b = strings.EqualFold(fmt.Sprint(cm["value"]), "true")
		}
		cm["value"] = b
	case "color":
		cm["value"] = normalizeHex6(strOr(cm["value"], "#78b9e8"), "#78b9e8")
	case "font":
		cm["value"] = normalizeBrandFont(strOr(cm["value"], "inter"), "inter")
	case "text", "image":
		cm["value"] = strOr(cm["value"], "")
	default:
		delete(seen, id)
		return false
	}
	cm["kind"] = kind
	return true
}

func repairSceneMap(sm map[string]any, roles map[string]float64, idx int, seenSceneIDs map[string]bool, controls map[string]bool) {
	doc, _ := sm["document"].(map[string]any)
	if doc == nil {
		doc = map[string]any{}
	}
	role := normalizeRole(strOr(sm["role"], ""))
	if role == "" {
		role = normalizeRole(strOr(doc["moment"], ""))
	}
	if role == "" {
		role = "ident"
	}
	sm["role"] = role

	id := strings.TrimSpace(strOr(sm["id"], ""))
	if id == "" {
		id = role
		if idx > 1 {
			id = fmt.Sprintf("%s-%d", role, idx)
		}
	}
	if seenSceneIDs != nil {
		id = uniqueID(id, seenSceneIDs)
	}
	sm["id"] = id

	if strings.TrimSpace(strOr(sm["name"], "")) == "" {
		sm["name"] = defaultRoleName(role)
	}
	if _, hasEnabled := sm["enabled"]; !hasEnabled {
		sm["enabled"] = true
	} else if b, ok := sm["enabled"].(bool); ok {
		sm["enabled"] = b
	} else {
		sm["enabled"] = true
	}

	w := int(math.Round(numOr(sm["weight"], 1)))
	if w < 1 {
		w = 1
	} else if w > 10 {
		w = 10
	}
	sm["weight"] = w

	dur := 8.0
	if roles != nil {
		if existing, ok := roles[role]; ok {
			dur = existing
		} else {
			dur = snapDuration(numOr(doc["duration"], 8))
			roles[role] = dur
		}
	} else {
		dur = snapDuration(numOr(doc["duration"], 8))
	}

	moment := role
	if m := strings.TrimSpace(strOr(doc["moment"], "")); strings.HasPrefix(m, "plugin:") || strings.HasPrefix(m, "screen.") {
		moment = m
	}
	repairDocumentMap(doc, moment, dur, controls)
	sm["document"] = doc

	if soundsRaw, ok := sm["sounds"].([]any); ok {
		seenSound := map[string]bool{}
		var sounds []any
		for si, item := range soundsRaw {
			if len(sounds) >= 4 {
				break
			}
			snd, ok := item.(map[string]any)
			if !ok {
				continue
			}
			digest := strings.TrimSpace(strOr(snd["assetDigest"], ""))
			if digest == "" {
				continue
			}
			sid := uniqueID(firstNonEmpty(strings.TrimSpace(strOr(snd["id"], "")), fmt.Sprintf("sound-%d", si+1)), seenSound)
			snd["id"] = sid
			snd["assetDigest"] = digest
			at := max(0, numOr(snd["at"], 0))
			if at >= dur {
				at = max(0, dur-0.5)
			}
			snd["at"] = at
			start := max(0, numOr(snd["start"], 0))
			snd["start"] = start
			if endVal, hasEnd := optFloat(snd["end"]); hasEnd && endVal > start {
				snd["end"] = endVal
			} else {
				delete(snd, "end")
			}
			vol := numOr(snd["volume"], 0.6)
			if math.IsNaN(vol) || math.IsInf(vol, 0) {
				vol = 0.6
			}
			snd["volume"] = max(0, min(1, vol))
			snd["fadeIn"] = max(0, numOr(snd["fadeIn"], 0))
			snd["fadeOut"] = max(0, numOr(snd["fadeOut"], 0))
			sounds = append(sounds, snd)
		}
		if sounds == nil {
			sounds = []any{}
		}
		sm["sounds"] = sounds
	} else {
		sm["sounds"] = []any{}
	}

	if vm, ok := sm["video"].(map[string]any); ok {
		digest := strings.TrimSpace(strOr(vm["assetDigest"], ""))
		if digest == "" {
			delete(sm, "video")
		} else {
			mode := strings.ToLower(strings.TrimSpace(strOr(vm["mode"], "background")))
			if mode != "background" && mode != "replace" {
				mode = "background"
			}
			fit := strings.ToLower(strings.TrimSpace(strOr(vm["fit"], "cover")))
			if fit != "cover" && fit != "contain" {
				fit = "cover"
			}
			vm["assetDigest"] = digest
			vm["mode"] = mode
			vm["fit"] = fit
			vm["start"] = max(0, numOr(vm["start"], 0))
		}
	} else {
		delete(sm, "video")
	}
}

var allowedDocKeys = map[string]bool{
	"audio": true, "v": true, "moment": true, "duration": true,
	"poster": true, "slots": true, "layers": true, "post": true,
}

func repairDocumentMap(doc map[string]any, moment string, dur float64, controls map[string]bool) {
	for k := range doc {
		if !allowedDocKeys[k] {
			delete(doc, k)
		}
	}
	doc["v"] = 1
	doc["moment"] = moment
	doc["duration"] = dur

	poster, hasPoster := optFloat(doc["poster"])
	if !hasPoster || poster < 0 || poster >= dur {
		poster = dur / 2
	}
	doc["poster"] = poster

	if am, ok := doc["audio"].(map[string]any); ok {
		gen := strings.ToLower(strings.TrimSpace(strOr(am["generator"], "")))
		if gen != "static" && gen != "tone" && gen != "bed" {
			delete(doc, "audio")
		} else {
			cleanAudio := map[string]any{"generator": gen}
			if seed, ok := optUint32(am["seed"]); ok && seed > 0 {
				cleanAudio["seed"] = seed
			}
			doc["audio"] = cleanAudio
		}
	} else {
		delete(doc, "audio")
	}

	budget := &sceneBudget{maxLayers: 200, maxParticles: 2000, seenIDs: map[string]bool{}, controls: controls}
	if layersRaw, ok := doc["layers"].([]any); ok {
		doc["layers"] = repairLayerSlice(layersRaw, dur, 0, 1, 0, budget)
	} else {
		doc["layers"] = []any{}
	}

	slotSet := map[string]bool{}
	if slotsRaw, ok := doc["slots"].([]any); ok {
		for _, item := range slotsRaw {
			if s := canonicalSlotName(strOr(item, ""), controls); s != "" && scene.IsLiveSlotKey(s) {
				slotSet[s] = true
			}
		}
	}
	if layersOut, ok := doc["layers"].([]any); ok {
		collectLiveSlotsFromLayers(layersOut, controls, slotSet)
	}
	if len(slotSet) > 0 {
		slots := make([]string, 0, len(slotSet))
		for s := range slotSet {
			slots = append(slots, s)
		}
		sort.Strings(slots)
		doc["slots"] = slots
	} else {
		delete(doc, "slots")
	}

	if postRaw, ok := doc["post"].([]any); ok {
		var post []any
		for _, item := range postRaw {
			pm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			pt := strings.ToLower(strings.TrimSpace(strOr(pm["type"], "")))
			if !sliceContains(scene.PostEffects, pt) {
				continue
			}
			amt := repairValue(pm["amount"], dur, false, false, controls)
			if amt == nil {
				amt = 0.12
			}
			clean := map[string]any{"type": pt, "amount": amt}
			if seed, ok := optUint32(pm["seed"]); ok && seed > 0 {
				clean["seed"] = seed
			}
			post = append(post, clean)
		}
		if post == nil {
			post = []any{}
		}
		doc["post"] = post
	} else {
		doc["post"] = []any{}
	}
}

type sceneBudget struct {
	layers       int
	particles    int
	maxLayers    int
	maxParticles int
	seq          int
	seenIDs      map[string]bool
	controls     map[string]bool
}

var allowedLayerKeys = map[string]bool{
	"name": true, "hidden": true, "locked": true, "start": true, "end": true,
	"id": true, "type": true, "x": true, "y": true, "w": true, "h": true,
	"scale": true, "scaleX": true, "scaleY": true, "rotate": true, "opacity": true,
	"fill": true, "stroke": true, "strokeWidth": true, "join": true, "cap": true,
	"fillRule": true, "path": true, "clip": true, "text": true, "font": true,
	"size": true, "weight": true, "track": true, "align": true, "maxWidth": true,
	"fit": true, "maxLines": true, "src": true, "tint": true, "tintBlend": true,
	"blend": true, "blur": true, "glow": true, "count": true, "seed": true,
	"repeat": true, "behave": true, "reveal": true, "layers": true,
}

func repairLayerSlice(raw []any, dur float64, depth, mult int, parentOffsetX float64, budget *sceneBudget) []any {
	if depth > 16 {
		return []any{}
	}
	out := make([]any, 0, len(raw))
	explicitAlign := make([]bool, 0, len(raw))
	for _, item := range raw {
		lm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if budget.layers >= budget.maxLayers {
			break
		}
		aliasKey(lm, "textAlign", "align")
		hadAlign := strings.TrimSpace(strOr(lm["align"], "")) != ""
		repaired, ok := repairLayerMap(lm, dur, depth, mult, parentOffsetX, budget)
		if ok {
			out = append(out, repaired)
			explicitAlign = append(explicitAlign, hadAlign)
		}
	}
	if math.Abs(parentOffsetX) <= 1 {
		repairCenteredTextSlice(out, explicitAlign)
	}
	return out
}

func repairLayerMap(lm map[string]any, dur float64, depth, mult int, parentOffsetX float64, budget *sceneBudget) (map[string]any, bool) {
	budget.seq++

	// Map common model aliases before stripping unsupported fields.
	aliasKey(lm, "behaviors", "behave")
	aliasKey(lm, "behaviours", "behave")
	aliasKey(lm, "children", "layers")
	aliasKey(lm, "fontFamily", "font")
	aliasKey(lm, "fontSize", "size")
	aliasKey(lm, "fontWeight", "weight")
	aliasKey(lm, "letterSpacing", "track")
	aliasKey(lm, "textAlign", "align")
	aliasKey(lm, "color", "fill")
	aliasKey(lm, "colour", "fill")
	aliasKey(lm, "d", "path")
	aliasKey(lm, "url", "src")
	aliasKey(lm, "href", "src")
	aliasKey(lm, "assetDigest", "src")

	repairLayerType(lm)
	ltype := strOr(lm["type"], "rect")

	rawID := strings.TrimSpace(strOr(lm["id"], ""))
	if rawID == "" {
		rawID = slugID(strOr(lm["name"], ""))
	}
	if rawID == "" {
		rawID = fmt.Sprintf("%s-%d", ltype, budget.seq)
	}
	id := uniqueID(rawID, budget.seenIDs)
	lm["id"] = id
	if strings.TrimSpace(strOr(lm["name"], "")) == "" {
		lm["name"] = humanizeID(id)
	}

	repeat := int(math.Round(numOr(lm["repeat"], 0)))
	repeat = max(0, min(200, repeat))
	maxAllowedRepeat := (budget.maxLayers - budget.layers) / max(1, mult)
	if maxAllowedRepeat < 1 {
		return nil, false
	}
	if repeat > maxAllowedRepeat {
		repeat = maxAllowedRepeat
	}
	if repeat > 0 {
		lm["repeat"] = repeat
	} else {
		delete(lm, "repeat")
	}
	n := max(1, repeat) * mult
	budget.layers += n

	count := int(math.Round(numOr(lm["count"], 0)))
	if ltype == "particles" && count <= 0 {
		count = 48
	}
	count = max(0, min(2000, count))
	maxAllowedParticles := (budget.maxParticles - budget.particles) / max(1, n)
	if count > maxAllowedParticles {
		count = max(0, maxAllowedParticles)
	}
	budget.particles += count * n
	if count > 0 {
		lm["count"] = count
	} else {
		delete(lm, "count")
	}

	start, hasStart := optFloat(lm["start"])
	if hasStart && start > 0 {
		start = min(dur, max(0, start))
		lm["start"] = start
	} else {
		start = 0
		delete(lm, "start")
	}
	if end, hasEnd := optFloat(lm["end"]); hasEnd && end > 0 {
		end = min(dur, max(0, end))
		if end <= start {
			delete(lm, "end")
		} else {
			lm["end"] = end
		}
	} else {
		delete(lm, "end")
	}

	for _, k := range []string{"blur", "glow"} {
		if v, ok := optFloat(lm[k]); ok && v > 0 {
			lm[k] = min(64, max(0, v))
		} else {
			delete(lm, k)
		}
	}
	if ml := int(math.Round(numOr(lm["maxLines"], 0))); ml > 0 {
		lm["maxLines"] = min(100, ml)
	} else {
		delete(lm, "maxLines")
	}
	if mw, ok := optFloat(lm["maxWidth"]); ok && mw > 0 {
		lm["maxWidth"] = mw
	} else {
		delete(lm, "maxWidth")
	}
	if wRaw, exists := lm["weight"]; exists {
		switch wv := wRaw.(type) {
		case string:
			if strings.EqualFold(wv, "bold") || strings.EqualFold(wv, "black") || strings.EqualFold(wv, "heavy") {
				lm["weight"] = 700
			} else if n, err := strconv.Atoi(strings.TrimSpace(wv)); err == nil && n > 0 {
				lm["weight"] = n
			} else {
				delete(lm, "weight")
			}
		default:
			if wn := int(math.Round(numOr(wRaw, 0))); wn > 0 {
				lm["weight"] = wn
			} else {
				delete(lm, "weight")
			}
		}
	}
	if seed, ok := optUint32(lm["seed"]); ok && seed > 0 {
		lm["seed"] = seed
	} else {
		delete(lm, "seed")
	}
	for _, bKey := range []string{"hidden", "locked"} {
		if b, ok := lm[bKey].(bool); ok && b {
			lm[bKey] = true
		} else {
			delete(lm, bKey)
		}
	}

	cleanEnum(lm, "blend", "normal multiply screen overlay", nil)
	cleanEnum(lm, "tintBlend", "normal multiply screen overlay silhouette", nil)
	if ltype == "text" {
		cleanEnum(lm, "fit", "shrink wrap clip", map[string]string{"contain": "shrink", "scale-down": "shrink"})
	} else if ltype == "image" || ltype == "art" || ltype == "logo" {
		cleanEnum(lm, "fit", "cover contain", nil)
	} else {
		cleanEnum(lm, "fit", "shrink wrap clip cover contain", nil)
	}
	cleanEnum(lm, "align", "left center right", map[string]string{"middle": "center", "start": "left", "end": "right"})
	cleanEnum(lm, "fillRule", "nonzero evenodd", nil)
	cleanEnum(lm, "join", "miter round bevel", nil)
	cleanEnum(lm, "cap", "butt round square", nil)

	for _, key := range []string{"x", "y", "w", "h", "scale", "scaleX", "scaleY", "rotate", "opacity", "strokeWidth", "text", "font", "size", "track", "src"} {
		if val, exists := lm[key]; exists {
			if repaired := repairValue(val, dur, false, key == "src", budget.controls); repaired != nil {
				lm[key] = repaired
			} else {
				delete(lm, key)
			}
		}
	}
	for _, key := range []string{"fill", "stroke", "tint"} {
		if val, exists := lm[key]; exists {
			if repaired := repairValue(val, dur, true, false, budget.controls); repaired != nil {
				lm[key] = repaired
			} else {
				delete(lm, key)
			}
		}
	}

	if src := strings.TrimSpace(strOr(lm["src"], "")); strings.Contains(src, "{next.poster}") || strings.Contains(src, "{next.backdrop}") {
		if ltype == "image" || ltype == "logo" {
			lm["type"] = "art"
			ltype = "art"
		}
	}
	if ltype == "art" && strings.TrimSpace(strOr(lm["src"], "")) == "" {
		return nil, false
	}

	// If a model treats text x/w as a centred CSS box spanning the safe area
	// (e.g. x:160, w:1600, align:"center"), convert x to the box midpoint and w to maxWidth.
	if ltype == "text" && strOr(lm["align"], "") == "center" {
		if xVal, okX := lm["x"].(float64); okX {
			if wVal, okW := lm["w"].(float64); okW && xVal >= 0 && xVal < 600 && wVal >= 600 && math.Abs((xVal+wVal/2)-(960-parentOffsetX)) <= 40 {
				lm["x"] = xVal + wVal/2
				if lm["maxWidth"] == nil {
					lm["maxWidth"] = wVal
				}
				delete(lm, "w")
			}
		}
	}

	for _, sKey := range []string{"path", "clip"} {
		if s, ok := lm[sKey].(string); ok && strings.TrimSpace(s) != "" {
			lm[sKey] = strings.TrimSpace(s)
		} else {
			delete(lm, sKey)
		}
	}
	if ltype == "path" && strOr(lm["path"], "") == "" {
		lm["type"] = "rect"
		ltype = "rect"
	}

	if beh, exists := lm["behave"]; exists {
		if cleaned := repairBehaviors(beh); len(cleaned) > 0 {
			lm["behave"] = cleaned
		} else {
			delete(lm, "behave")
		}
	}
	if rev, ok := lm["reveal"].(map[string]any); ok {
		if cleaned, valid := repairReveal(rev, dur); valid {
			lm["reveal"] = cleaned
		} else {
			delete(lm, "reveal")
		}
	} else {
		delete(lm, "reveal")
	}

	if children, ok := lm["layers"].([]any); ok && ltype == "group" {
		groupX, okX := settledCoord(lm["x"])
		if !okX {
			if lm["x"] == nil {
				groupX = 0
			} else {
				groupX = 1e6
			}
		}
		lm["layers"] = repairLayerSlice(children, dur, depth+1, n, parentOffsetX+groupX, budget)
	} else {
		delete(lm, "layers")
	}

	for k := range lm {
		if !allowedLayerKeys[k] {
			delete(lm, k)
		}
	}
	return lm, true
}

// repairCenteredTextSlice sets align:"center" on text layers placed near the
// 1920px canvas horizontal midpoint (x ≈ 960) when align was omitted and the
// surrounding slice clearly uses a centred stack rather than a two-column split.
func repairCenteredTextSlice(layers []any, explicitAlign []bool) {
	for i, item := range layers {
		lm, ok := item.(map[string]any)
		if !ok || strOr(lm["type"], "") != "text" {
			continue
		}
		if (i < len(explicitAlign) && explicitAlign[i]) || lm["align"] != nil {
			continue
		}
		x, okX := settledCoord(lm["x"])
		if !okX || math.Abs(x-960) > 32 {
			continue
		}
		y, hasY := settledCoord(lm["y"])
		mw, hasMW := optFloat(lm["maxWidth"])
		overflowsRight := hasMW && x+mw > 1800

		sameRowLeftText := false
		leftColumnCard := false
		if hasY {
			for j, other := range layers {
				if j == i {
					continue
				}
				om, ok := other.(map[string]any)
				if !ok {
					continue
				}
				otype := strOr(om["type"], "")
				ox, okOX := settledCoord(om["x"])
				oy, okOY := settledCoord(om["y"])
				if !okOX || !okOY {
					continue
				}
				if otype == "text" && ox < 880 && math.Abs(oy-y) <= 12 {
					sameRowLeftText = true
					break
				}
				if otype == "art" || otype == "image" || otype == "rect" {
					ow, okOW := optFloat(om["w"])
					oh, okOH := optFloat(om["h"])
					if okOW && okOH && ox >= 60 && ow >= 200 && ox+ow <= 940 && y >= oy && y <= oy+oh {
						leftColumnCard = true
					}
				}
			}
		}
		if sameRowLeftText {
			continue
		}
		if leftColumnCard && !overflowsRight {
			continue
		}
		lm["align"] = "center"
	}
}

func settledCoord(v any) (float64, bool) {
	if v == nil {
		return 0, true
	}
	if f, ok := optFloat(v); ok {
		return f, true
	}
	m, ok := v.(map[string]any)
	if !ok {
		return 0, false
	}
	keys, ok := m["keys"].([]any)
	if !ok || len(keys) == 0 {
		return 0, false
	}
	var vals []float64
	for _, k := range keys {
		arr, ok := k.([]any)
		if !ok || len(arr) < 2 {
			return 0, false
		}
		fv, ok := optFloat(arr[1])
		if !ok {
			return 0, false
		}
		vals = append(vals, fv)
	}
	if len(vals) == 0 {
		return 0, false
	}
	mid := vals[len(vals)/2]
	for _, fv := range vals {
		if math.Abs(fv-mid) > 80 {
			return 0, false
		}
	}
	return mid, true
}

// repairLayerType maps unsupported or hallucinated layer types (most notably
// "line") onto valid renderer primitives before scene.Decode runs.
func repairLayerType(lm map[string]any) {
	t := strings.ToLower(strings.TrimSpace(strOr(lm["type"], "")))
	if sliceContains(scene.LayerTypes, t) {
		lm["type"] = t
		return
	}
	switch t {
	case "line":
		_, hasX1 := lm["x1"]
		_, hasY1 := lm["y1"]
		_, hasX2 := lm["x2"]
		_, hasY2 := lm["y2"]
		if hasX1 || hasY1 || hasX2 || hasY2 {
			x1, y1 := numOr(lm["x1"], 0), numOr(lm["y1"], 0)
			x2, y2 := numOr(lm["x2"], 400), numOr(lm["y2"], y1)
			lm["type"] = "path"
			lm["x"] = 0.0
			lm["y"] = 0.0
			lm["path"] = fmt.Sprintf("M %g %g L %g %g", x1, y1, x2, y2)
			promoteFillToStroke(lm)
			return
		}
		if strings.TrimSpace(strOr(lm["path"], "")) != "" {
			lm["type"] = "path"
			promoteFillToStroke(lm)
			return
		}
		if lm["fill"] != nil && lm["stroke"] == nil {
			lm["type"] = "rect"
			if lm["w"] == nil {
				lm["w"] = 400.0
			}
			if lm["h"] == nil {
				lm["h"] = 2.0
			}
			return
		}
		w := numOr(lm["w"], 400)
		h := numOr(lm["h"], 0)
		lm["type"] = "path"
		lm["path"] = fmt.Sprintf("M 0 0 L %g %g", w, h)
		delete(lm, "w")
		delete(lm, "h")
		promoteFillToStroke(lm)
	case "box", "rectangle", "bar", "panel", "card", "frame", "scrim", "divider", "rule", "background":
		lm["type"] = "rect"
	case "circle", "oval", "ring", "dot":
		if r, ok := optFloat(firstPresent(lm, "r", "radius")); ok && r > 0 && lm["w"] == nil && lm["h"] == nil {
			lm["w"] = r * 2
			lm["h"] = r * 2
		}
		lm["type"] = "ellipse"
	case "svg", "shape", "polygon", "polyline", "curve":
		if strings.TrimSpace(strOr(lm["path"], "")) != "" {
			lm["type"] = "path"
		} else {
			lm["type"] = "rect"
		}
	case "label", "title", "heading", "subtitle", "caption", "copy", "kicker":
		lm["type"] = "text"
	case "photo", "picture", "poster", "backdrop", "artwork":
		if strings.Contains(strOr(lm["src"], ""), "{next.") {
			lm["type"] = "art"
		} else {
			lm["type"] = "image"
		}
	case "monogram", "watermark", "badge":
		if lm["src"] != nil {
			lm["type"] = "logo"
		} else {
			lm["type"] = "rect"
		}
	case "container", "folder":
		lm["type"] = "group"
	case "static", "grain":
		lm["type"] = "noise"
	default:
		if _, hasLayers := lm["layers"].([]any); hasLayers {
			lm["type"] = "group"
		} else if lm["text"] != nil {
			lm["type"] = "text"
		} else if lm["src"] != nil {
			lm["type"] = "image"
		} else if strings.TrimSpace(strOr(lm["path"], "")) != "" {
			lm["type"] = "path"
		} else {
			lm["type"] = "rect"
		}
	}
}

func promoteFillToStroke(lm map[string]any) {
	if lm["stroke"] == nil {
		if lm["fill"] != nil {
			lm["stroke"] = lm["fill"]
			delete(lm, "fill")
		} else {
			lm["stroke"] = "{accent}"
		}
	}
	if lm["strokeWidth"] == nil {
		lm["strokeWidth"] = 2.0
	}
}

func repairValue(v any, dur float64, isColor, isSrc bool, controls map[string]bool) any {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		s := repairBindingString(x, isColor, isSrc, controls)
		if isColor {
			s = normalizePaintColor(s)
			if s == "" {
				return nil
			}
		}
		return s
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return x
	case int:
		return float64(x)
	case bool:
		return fmt.Sprint(x)
	case map[string]any:
		if keysRaw, ok := x["keys"].([]any); ok {
			maxT := min(30.0, dur)
			type kf struct {
				t    float64
				val  any
				ease string
			}
			var frames []kf
			for _, item := range keysRaw {
				arr, ok := item.([]any)
				if !ok || len(arr) < 2 {
					continue
				}
				kt, ok := optFloat(arr[0])
				if !ok {
					continue
				}
				kt = min(maxT, max(0, kt))
				if _, nested := arr[1].(map[string]any); nested {
					continue
				}
				kv := repairValue(arr[1], dur, isColor, isSrc, controls)
				if kv == nil {
					continue
				}
				ease := ""
				if len(arr) >= 3 {
					ease = normalizeEase(fmt.Sprint(arr[2]))
				}
				frames = append(frames, kf{t: kt, val: kv, ease: ease})
			}
			if len(frames) == 0 {
				return nil
			}
			sort.SliceStable(frames, func(i, j int) bool { return frames[i].t < frames[j].t })
			if len(frames) > 256 {
				frames = frames[:256]
			}
			out := make([]any, 0, len(frames))
			for _, f := range frames {
				if f.ease != "" {
					out = append(out, []any{f.t, f.val, f.ease})
				} else {
					out = append(out, []any{f.t, f.val})
				}
			}
			return map[string]any{"keys": out}
		}

		linearRaw, hasLinear := x["linear"].([]any)
		radialRaw, hasRadial := x["radial"].([]any)
		if !hasLinear && !hasRadial {
			return nil
		}
		kind := "linear"
		coordsRaw := linearRaw
		fallbackCoords := []float64{0, 0, 0, 1080}
		if !hasLinear && hasRadial {
			kind = "radial"
			coordsRaw = radialRaw
			fallbackCoords = []float64{960, 540, 0, 960}
		}
		coords := make([]any, 4)
		for i := 0; i < 4; i++ {
			if i < len(coordsRaw) {
				if c, ok := optFloat(coordsRaw[i]); ok {
					coords[i] = c
					continue
				}
			}
			coords[i] = fallbackCoords[i]
		}
		stopsRaw, _ := x["stops"].([]any)
		type gstop struct {
			at  float64
			col string
		}
		var stops []gstop
		for _, item := range stopsRaw {
			arr, ok := item.([]any)
			if !ok || len(arr) < 2 {
				continue
			}
			at, ok := optFloat(arr[0])
			if !ok {
				continue
			}
			col := normalizePaintColor(repairBindingString(fmt.Sprint(arr[1]), true, false, controls))
			if col == "" {
				continue
			}
			stops = append(stops, gstop{at: min(1, max(0, at)), col: col})
		}
		if len(stops) < 2 {
			return nil
		}
		sort.SliceStable(stops, func(i, j int) bool { return stops[i].at < stops[j].at })
		if len(stops) > 64 {
			stops = stops[:64]
		}
		outStops := make([]any, 0, len(stops))
		for _, st := range stops {
			outStops = append(outStops, []any{st.at, st.col})
		}
		return map[string]any{kind: coords, "stops": outStops}
	}
	return nil
}

// repairBindingString rewrites unsupported model slot/token forms (dot-indexed
// or aliased schedule arrays, bracket property access, snake/camel/custom
// {words.*} tokens, and common slot aliases) onto the binder's supported grammar,
// dropping unknown placeholders cleanly instead of leaving unresolvable slots.
func repairBindingString(s string, isColor, isSrc bool, controls map[string]bool) string {
	if !strings.Contains(s, "{") {
		return s
	}
	// Collapse Mustache-style double braces {{slot}} -> {slot}.
	for strings.Contains(s, "{{") && strings.Contains(s, "}}") {
		next := strings.ReplaceAll(strings.ReplaceAll(s, "{{", "{"), "}}", "}")
		if next == s {
			break
		}
		s = next
	}
	var out strings.Builder
	droppedAny := false
	rem := s
	for {
		start := strings.IndexByte(rem, '{')
		if start < 0 {
			out.WriteString(rem)
			break
		}
		endRel := strings.IndexByte(rem[start:], '}')
		if endRel < 0 {
			out.WriteString(rem)
			break
		}
		out.WriteString(rem[:start])
		rawKey := strings.TrimSpace(rem[start+1 : start+endRel])
		rem = rem[start+endRel+1:]

		alphaSuffix := ""
		if at := strings.IndexByte(rawKey, '@'); at >= 0 {
			alphaSuffix = rawKey[at:]
			rawKey = strings.TrimSpace(rawKey[:at])
		}
		repl, dropped := rewriteSlotPlaceholder(rawKey, isColor, isSrc, controls)
		if dropped {
			droppedAny = true
			if alphaSuffix != "" && strings.HasPrefix(rem, "@") {
				// Nothing to strip; rem already carries its own suffix.
			}
			continue
		}
		out.WriteString(repl)
		if alphaSuffix != "" && strings.HasPrefix(repl, "{") && strings.HasSuffix(repl, "}") {
			out.WriteString(alphaSuffix)
		}
	}
	res := out.String()
	if droppedAny && !isColor && !isSrc {
		res = cleanDroppedSeparators(res)
	}
	return res
}

func rewriteSlotPlaceholder(rawKey string, isColor, isSrc bool, controls map[string]bool) (string, bool) {
	key := strings.TrimPrefix(strings.TrimSpace(rawKey), "$")
	if key == "" {
		return "", true
	}
	key = normalizeBracketProps(key)
	if key == "type.mono" {
		return "{type.body}", false
	}
	if scene.IsSupportedBinding(key, controls) {
		return "{" + key + "}", false
	}

	// Array / schedule row forms: list.0.title, schedule[0].title, items[1].time, etc.
	if arrName, idxStr, field, ok := parseIndexedSlot(key); ok {
		switch arrName {
		case "list", "schedule", "lineup", "items", "rows", "programmes", "programs", "upcoming", "tonight", "slots", "entries":
			idx, err := strconv.Atoi(idxStr)
			if err != nil || idx < 0 || idx > 3 {
				return "", true
			}
			switch normTokenPart(field) {
			case "", "title", "name", "label", "show", "programme", "program":
				return fmt.Sprintf("{list[%d].title}", idx), false
			case "time", "start", "at", "hour", "clock":
				return fmt.Sprintf("{list[%d].time}", idx), false
			case "meta", "subtitle", "sub", "info", "detail", "details", "summary", "description", "logline", "genre", "duration", "year":
				return fmt.Sprintf("{list[%d].meta}", idx), false
			case "poster", "thumb", "thumbnail":
				return "{next.poster}", false
			case "backdrop", "art", "image", "banner":
				return "{next.backdrop}", false
			default:
				return "", true
			}
		}
	}

	lower := strings.ToLower(key)

	// {words.*} and {word.*} forms: map onto SupportedWordTokens or humanize custom labels.
	if strings.HasPrefix(lower, "words.") || strings.HasPrefix(lower, "word.") {
		sub := key[strings.IndexByte(key, '.')+1:]
		switch normTokenPart(sub) {
		case "upnext", "next", "comingup":
			return "{words.upNext}", false
		case "tonight", "lineup", "schedule", "evening":
			return "{words.tonight}", false
		case "now", "nowplaying", "playing", "onair", "live":
			return "{words.now}", false
		case "break", "intermission", "commercial", "commercials", "adbreak", "standby", "pause":
			return "{words.break}", false
		case "back", "return", "resuming", "nowresuming", "welcomeback", "backfrombreak":
			return "{words.back}", false
		case "offair", "closedown", "signoff", "goodnight", "closed":
			return "{words.offair}", false
		case "tagline", "slogan", "motto", "subtitle":
			return "{words.tagline}", false
		case "welcome", "greeting", "hello":
			return "{viewer.greeting}", false
		case "channel", "station", "name":
			return "{channel.name}", false
		default:
			if isColor || isSrc {
				return "", true
			}
			label := humanizeWordsToken(sub)
			if label == "" {
				return "", true
			}
			return label, false
		}
	}

	// Brand / control / top-level slot aliases.
	switch normTokenPart(key) {
	case "bg", "background", "palettebackground", "brandpalettebackground":
		return "{bg}", false
	case "accent", "paletteaccent", "brandpaletteaccent", "brandaccent", "channelaccent":
		return "{accent}", false
	case "text", "foreground", "fg", "palettetext", "brandpalettetext":
		return "{text}", false
	case "glow", "paletteglow", "brandpaletteglow", "brandglow":
		return "{glow}", false
	case "energy", "brandenergy":
		return "{energy}", false
	case "typedisplay", "fontdisplay", "brandtypedisplay", "displayfont":
		return "{type.display}", false
	case "typebody", "typemono", "fontbody", "fontmono", "brandtypebody", "bodyfont":
		return "{type.body}", false
	case "tagline", "brandtagline", "channeltagline", "stationtagline":
		return "{words.tagline}", false
	case "channel", "channelname", "channeltitle", "station", "stationname", "brandname":
		return "{channel.name}", false
	case "logo", "brandlogo", "channellogo", "stationlogo":
		return "{brand.logo}", false
	case "offairmessage", "offairtext", "offairsubtitle", "offairreason":
		return "{offair.message}", false
	case "viewer", "viewername", "viewertitle", "username", "user":
		return "{viewer.name}", false
	case "viewergreeting", "usergreeting", "greeting", "welcomegreeting":
		return "{viewer.greeting}", false
	case "lineupheading", "tonightheading", "scheduleheading":
		return "{words.tonight}", false
	}

	if id, ok := strings.CutPrefix(key, "controls."); ok && id != "" {
		if controls == nil || controls[id] {
			return "{control." + id + "}", false
		}
		return "", true
	}
	if tex, ok := strings.CutPrefix(lower, "texture."); ok {
		cand := "textures." + tex
		if scene.IsSupportedBinding(cand, controls) {
			return "{" + cand + "}", false
		}
	}

	// Prefix-qualified programme slots: next.*, upnext.*, upcoming.*, now.*, current.*, etc.
	if prefix, field, ok := strings.Cut(key, "."); ok {
		switch normTokenPart(prefix) {
		case "next", "upnext", "upcoming", "list":
			if mapped := mapProgrammeField("next", field); mapped != "" {
				return "{" + mapped + "}", false
			}
		case "now", "current", "playing", "programme", "program", "show":
			if mapped := mapProgrammeField("now", field); mapped != "" {
				return "{" + mapped + "}", false
			}
		}
	}

	return "", true
}

func mapProgrammeField(target, field string) string {
	switch normTokenPart(field) {
	case "title", "name", "label":
		return target + ".title"
	case "meta", "subtitle", "sub", "info", "detail", "details", "genre", "year", "duration":
		return target + ".meta"
	case "logline", "summary", "description", "synopsis", "plot":
		return target + ".logline"
	case "time", "start", "at", "hour":
		return "next.time"
	case "reason", "because", "note":
		return "next.reason"
	case "poster", "thumb", "thumbnail":
		return "next.poster"
	case "backdrop", "art", "image", "banner":
		return "next.backdrop"
	}
	return ""
}

func canonicalSlotName(raw string, controls map[string]bool) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(strings.TrimSuffix(s, "}"), "{")
	repl, dropped := rewriteSlotPlaceholder(s, false, false, controls)
	if dropped || !strings.HasPrefix(repl, "{") || !strings.HasSuffix(repl, "}") {
		return ""
	}
	return repl[1 : len(repl)-1]
}

func collectLiveSlotsFromLayers(layers []any, controls map[string]bool, dst map[string]bool) {
	var visitValue func(any)
	visitValue = func(v any) {
		switch x := v.(type) {
		case string:
			rem := x
			for {
				start := strings.IndexByte(rem, '{')
				if start < 0 {
					return
				}
				endRel := strings.IndexByte(rem[start:], '}')
				if endRel < 0 {
					return
				}
				key := rem[start+1 : start+endRel]
				if at := strings.IndexByte(key, '@'); at >= 0 {
					key = key[:at]
				}
				if scene.IsLiveSlotKey(key) && scene.IsSupportedBinding(key, controls) {
					dst[key] = true
				}
				rem = rem[start+endRel+1:]
			}
		case map[string]any:
			for _, child := range x {
				visitValue(child)
			}
		case []any:
			for _, child := range x {
				visitValue(child)
			}
		}
	}
	for _, item := range layers {
		lm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for k, v := range lm {
			if k == "layers" {
				if children, ok := v.([]any); ok {
					collectLiveSlotsFromLayers(children, controls, dst)
				}
				continue
			}
			visitValue(v)
		}
	}
}

// normalizeBracketProps rewrites quoted bracket property access such as
// next["title"] or list[0]['title'] into dot property access while preserving
// integer array indices like list[0].title.
func normalizeBracketProps(key string) string {
	var b strings.Builder
	for i := 0; i < len(key); {
		if key[i] == '[' {
			end := strings.IndexByte(key[i:], ']')
			if end > 1 {
				inner := strings.TrimSpace(key[i+1 : i+end])
				unquoted := strings.Trim(inner, `"'`)
				if unquoted != inner && unquoted != "" {
					if b.Len() > 0 {
						b.WriteByte('.')
					}
					b.WriteString(unquoted)
					i += end + 1
					continue
				}
				if _, err := strconv.Atoi(inner); err != nil && inner != "" {
					if b.Len() > 0 {
						b.WriteByte('.')
					}
					b.WriteString(inner)
					i += end + 1
					continue
				}
			}
		}
		b.WriteByte(key[i])
		i++
	}
	return b.String()
}

func parseIndexedSlot(key string) (arr, idx, field string, ok bool) {
	// Bracket form: arr[0].field or arr[0]
	if lb := strings.IndexByte(key, '['); lb > 0 {
		rb := strings.IndexByte(key[lb:], ']')
		if rb > 1 {
			arr = strings.ToLower(strings.TrimSpace(key[:lb]))
			idx = strings.TrimSpace(key[lb+1 : lb+rb])
			rest := strings.TrimPrefix(key[lb+rb+1:], ".")
			if _, err := strconv.Atoi(idx); err == nil {
				return arr, idx, rest, true
			}
		}
	}
	// Dot form: arr.0.field or arr.0
	parts := strings.SplitN(key, ".", 3)
	if len(parts) >= 2 {
		if _, err := strconv.Atoi(parts[1]); err == nil {
			if len(parts) == 3 {
				return strings.ToLower(parts[0]), parts[1], parts[2], true
			}
			return strings.ToLower(parts[0]), parts[1], "", true
		}
	}
	return "", "", "", false
}

func normTokenPart(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func humanizeWordsToken(s string) string {
	var b strings.Builder
	var prev rune
	for _, r := range strings.TrimSpace(s) {
		if r == '_' || r == '-' || r == '.' || unicode.IsSpace(r) {
			if b.Len() > 0 && prev != ' ' {
				b.WriteByte(' ')
				prev = ' '
			}
			continue
		}
		if unicode.IsUpper(r) && b.Len() > 0 && prev != ' ' && unicode.IsLower(prev) {
			b.WriteByte(' ')
		}
		b.WriteRune(unicode.ToUpper(r))
		prev = r
	}
	return strings.TrimSpace(b.String())
}

func cleanDroppedSeparators(s string) string {
	for _, sep := range []string{"·", "|", "—", "–", "/"} {
		for {
			next := strings.ReplaceAll(s, sep+" "+sep, sep)
			next = strings.ReplaceAll(next, sep+sep, sep)
			if next == s {
				break
			}
			s = next
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, " ·|—–/-:")
	return strings.TrimSpace(s)
}

func repairBehaviors(raw any) []any {
	var list []any
	switch x := raw.(type) {
	case []any:
		list = x
	case map[string]any:
		list = []any{x}
	default:
		return nil
	}
	var out []any
	for _, item := range list {
		bm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, kind := range scene.BehaviorKinds {
			pm, ok := bm[kind].(map[string]any)
			if !ok {
				continue
			}
			params := map[string]any{}
			if v, ok := optFloat(pm["start"]); ok && v > 0 {
				params["start"] = v
			}
			if v, ok := optFloat(pm["step"]); ok && v >= 0 {
				params["step"] = v
			}
			if v, ok := optFloat(pm["amplitude"]); ok {
				params["amplitude"] = v
			}
			if v, ok := optFloat(pm["speed"]); ok && v >= 0 {
				params["speed"] = v
			}
			if seed, ok := optUint32(pm["seed"]); ok && seed > 0 {
				params["seed"] = seed
			}
			out = append(out, map[string]any{kind: params})
		}
	}
	return out
}

func repairReveal(rev map[string]any, dur float64) (map[string]any, bool) {
	wipe := strings.ToLower(strings.TrimSpace(strOr(rev["wipe"], "ltr")))
	if wipe != "ltr" && wipe != "rtl" {
		wipe = "ltr"
	}
	from := max(0, numOr(rev["from"], 0))
	to := numOr(rev["to"], from+0.8)
	if to <= from {
		to = from + 0.8
	}
	clean := map[string]any{"wipe": wipe, "from": from, "to": to}
	if b, ok := rev["out"].(bool); ok && b {
		clean["out"] = true
	}
	if ease := normalizeEase(strOr(rev["ease"], "")); ease != "" {
		clean["ease"] = ease
	}
	_ = dur
	return clean, true
}

func normalizeEase(s string) string {
	s = strings.TrimSpace(s)
	if sliceContains(scene.Easings, s) {
		return s
	}
	switch strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "-", ""), "_", "")) {
	case "linear":
		return "linear"
	case "in", "easein", "cubicin":
		return "in"
	case "out", "easeout", "cubicout", "ease":
		return "out"
	case "inout", "easeinout", "cubicinout":
		return "inOut"
	case "back", "easeoutback":
		return "back"
	case "elastic":
		return "elastic"
	case "spring":
		return "spring"
	}
	return "out"
}

func normalizeRole(s string) string {
	r := strings.ToLower(strings.TrimSpace(s))
	r = strings.NewReplacer("-", "", "_", "", " ", "").Replace(r)
	switch r {
	case "ident", "stationid":
		return "ident"
	case "upnext", "next", "comingup":
		return "upnext"
	case "tonight", "lineup", "schedule":
		return "tonight"
	case "break", "intermission", "commercialbreak":
		return "break"
	case "back", "backfrombreak", "return", "resuming":
		return "back"
	case "offair", "closed", "signoff":
		return "offair"
	case "welcome", "greeting":
		return "welcome"
	case "lowerthird", "banner":
		return "lowerthird"
	case "bug", "cornerlogo", "corner", "watermark":
		return "bug"
	}
	return ""
}

func defaultRoleName(role string) string {
	switch role {
	case "ident":
		return "Ident"
	case "upnext":
		return "Up next"
	case "tonight":
		return "Tonight lineup"
	case "break":
		return "Break bumper"
	case "back":
		return "Back from break"
	case "offair":
		return "Off air slate"
	case "welcome":
		return "Welcome card"
	case "lowerthird":
		return "Lower third"
	case "bug":
		return "Corner logo"
	default:
		return humanizeID(role)
	}
}

func snapDuration(d float64) float64 {
	if math.IsNaN(d) || math.IsInf(d, 0) || d <= 0 {
		return 8
	}
	snapped := math.Round(d/4) * 4
	return max(4, min(28, snapped))
}

func normalizeHex6(s, fallback string) string {
	s = strings.TrimSpace(s)
	if at := strings.Index(s, "@"); at >= 0 {
		s = s[:at]
	}
	if !strings.HasPrefix(s, "#") {
		return fallback
	}
	hexPart := s[1:]
	if len(hexPart) == 3 && isHexString(hexPart) {
		return strings.ToLower(fmt.Sprintf("#%c%c%c%c%c%c", hexPart[0], hexPart[0], hexPart[1], hexPart[1], hexPart[2], hexPart[2]))
	}
	if len(hexPart) == 8 && isHexString(hexPart) {
		return "#" + strings.ToLower(hexPart[:6])
	}
	if len(hexPart) == 6 && isHexString(hexPart) {
		return "#" + strings.ToLower(hexPart)
	}
	return fallback
}

func normalizePaintColor(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#") {
		return s
	}
	suffix := ""
	if at := strings.Index(s, "@"); at >= 0 {
		suffix = s[at:]
		s = s[:at]
	}
	hexPart := s[1:]
	if len(hexPart) == 3 && isHexString(hexPart) {
		return strings.ToLower(fmt.Sprintf("#%c%c%c%c%c%c", hexPart[0], hexPart[0], hexPart[1], hexPart[1], hexPart[2], hexPart[2])) + suffix
	}
	return s + suffix
}

func ensureContrastHex(bgHex, fgHex string) string {
	bgLum, ok1 := hexLuminance(bgHex)
	fgLum, ok2 := hexLuminance(fgHex)
	if !ok1 || !ok2 {
		return fgHex
	}
	if contrastRatio(bgLum, fgLum) >= 4.5 {
		return fgHex
	}
	r, g, b := parseHexRGB(fgHex)
	tr, tg, tb := 248.0, 250.0, 252.0
	if bgLum > 0.18 {
		tr, tg, tb = 12.0, 15.0, 22.0
	}
	for step := 1; step <= 20; step++ {
		u := float64(step) / 20.0
		nr := uint8(math.Round(r + (tr-r)*u))
		ng := uint8(math.Round(g + (tg-g)*u))
		nb := uint8(math.Round(b + (tb-b)*u))
		cand := fmt.Sprintf("#%02x%02x%02x", nr, ng, nb)
		if cl, ok := hexLuminance(cand); ok && contrastRatio(bgLum, cl) >= 4.6 {
			return cand
		}
	}
	if bgLum > 0.18 {
		return "#0c0f16"
	}
	return "#f8fafc"
}

func hexLuminance(v string) (float64, bool) {
	if len(v) != 7 || v[0] != '#' {
		return 0, false
	}
	b, err := hex.DecodeString(v[1:])
	if err != nil {
		return 0, false
	}
	var x [3]float64
	for i, n := range b {
		c := float64(n) / 255
		if c <= .04045 {
			c /= 12.92
		} else {
			c = math.Pow((c+.055)/1.055, 2.4)
		}
		x[i] = c
	}
	return .2126*x[0] + .7152*x[1] + .0722*x[2], true
}

func parseHexRGB(v string) (float64, float64, float64) {
	b, err := hex.DecodeString(strings.TrimPrefix(v, "#"))
	if err != nil || len(b) < 3 {
		return 240, 244, 248
	}
	return float64(b[0]), float64(b[1]), float64(b[2])
}

func contrastRatio(a, b float64) float64 {
	return (math.Max(a, b) + .05) / (math.Min(a, b) + .05)
}

func isHexString(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return len(s) > 0
}

func aliasKey(m map[string]any, from, to string) {
	if v, ok := m[from]; ok {
		if _, exists := m[to]; !exists {
			m[to] = v
		}
		delete(m, from)
	}
}

func cleanEnum(m map[string]any, key, allowed string, aliases map[string]string) {
	raw, exists := m[key]
	if !exists {
		return
	}
	s := strings.TrimSpace(fmt.Sprint(raw))
	if s == "" {
		delete(m, key)
		return
	}
	for _, item := range strings.Fields(allowed) {
		if strings.EqualFold(s, item) {
			m[key] = item
			return
		}
	}
	if mapped, ok := aliases[strings.ToLower(s)]; ok {
		m[key] = mapped
		return
	}
	delete(m, key)
}

func uniqueID(base string, seen map[string]bool) string {
	if !seen[base] {
		seen[base] = true
		return base
	}
	for n := 2; ; n++ {
		cand := fmt.Sprintf("%s-%d", base, n)
		if !seen[cand] {
			seen[cand] = true
			return cand
		}
	}
}

func slugID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func humanizeID(s string) string {
	s = strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(s))
	if s == "" {
		return "Layer"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstPresent(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

func sliceContains(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func strOr(v any, fallback string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fallback
}

func numOr(v any, fallback float64) float64 {
	if f, ok := optFloat(v); ok {
		return f
	}
	return fallback
}

func optFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

func optUint32(v any) (uint32, bool) {
	f, ok := optFloat(v)
	if !ok || f < 0 || f > math.MaxUint32 {
		return 0, false
	}
	return uint32(f), true
}
