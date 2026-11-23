package onair

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/llm"
	"github.com/braedonsaunders/ether/internal/onair/families"
	"github.com/braedonsaunders/ether/internal/onair/scene"
)

// Chatter is the owner's configured model. Proposals never save records.
type Chatter interface {
	Enabled() bool
	Chat(context.Context, []llm.Message, bool) (string, error)
}

func (s *Service) Draft(ctx context.Context, l Look) (Look, error) {
	l.Pack = nil
	l.Render = nil
	repaired, err := RepairLook(l)
	if err != nil {
		return Look{}, err
	}
	l = repaired
	for i := range l.Scenes {
		cleanOverlayScene(&l.Scenes[i])
		b, e := json.Marshal(l.Scenes[i].Document)
		if e != nil {
			return Look{}, e
		}
		d, e := scene.Decode(b)
		if e != nil {
			return Look{}, fmt.Errorf("scene %q: %w", l.Scenes[i].Name, e)
		}
		l.Scenes[i].Document = d
	}
	return l, s.validateLook(ctx, l)
}

// cleanOverlayScene keeps lowerthird and bug scenes transparent over live
// programme video by clearing full-frame post effects and dropping accidental
// 1920x1080 background plates.
func cleanOverlayScene(sc *StyleScene) {
	if sc.Role != "lowerthird" && sc.Role != "bug" {
		return
	}
	sc.Document.Post = nil
	for len(sc.Document.Layers) > 1 {
		first := sc.Document.Layers[0]
		if first.Type != "rect" && first.Type != "gradient" {
			break
		}
		w, okW := first.W.(float64)
		h, okH := first.H.(float64)
		if !okW || !okH || w < 1800 || h < 1000 {
			break
		}
		sc.Document.Layers = sc.Document.Layers[1:]
	}
}

// StyleDraftGuide is the renderer contract shared by generation and live agent
// drafts. Its supported layer, easing, post-effect, behaviour, font, role, slot,
// and word-token lists come straight from the binder and validator tables so
// the guide cannot drift.
var StyleDraftGuide = buildStyleDraftGuide()

func buildStyleDraftGuide() string {
	return fmt.Sprintf(`A style owns every scene document on a 1920x1080 canvas. Preserve existing IDs, channelIds and unrelated content when refining.
Top-level shape: {id,name,template,brand:{palette:{background,accent,text,glow},type:{display,body},logo,tagline,energy:0..1,textures:[grain|flicker|scanlines|vignette|chroma],language},roles:{<role>:{duration:8}},scenes:[{id,role,name,enabled:true,weight:1..10,document:{v:1,moment,duration,poster,slots?,layers:[],post:[]},sounds:[{id,assetDigest,at,start,end?,volume,fadeIn,fadeOut}],video?:{assetDigest,mode:background|replace,fit:cover|contain,start}}],controls:[{id,label,kind:color|font|text|number|toggle|image,value,min?,max?,step?,group?}],channelIds:[]}.

Required roles (%d): %s.
Every complete style MUST include all %d roles in both roles and scenes, with at least one enabled (enabled:true), non-empty designed scene per role. Role durations are multiples of 4 between 4 and 28 seconds; each scene.document.duration must equal roles[scene.role].duration and scene.document.moment must equal scene.role.
Supported slots: %s.
Word tokens: %s.
Brand tokens: %s.
Slot grammar: Use ONLY exact {<slot>} placeholders from Supported slots, Word tokens, Brand tokens, {textures.<kind>}, or declared {control.<id>}. Schedule rows use 0-based bracket indexing 0..3 ONLY ({list[0].time}, {list[0].title}, {list[0].meta} through {list[3].time}, {list[3].title}, {list[3].meta}); never invent other slot names, dot indices like list.0.title, or indices >= 4.
Role intent, durations, layout mode and live slots:
- ident (4-12s, usually 8s, full-screen): Flagship station ID establishing channel personality. Use {channel.name}, {words.tagline}, optional {brand.logo} (only when brand.logo is non-empty).
- upnext (8-12s, usually 8s, full-screen): Next programme promo card. Use {words.upNext}, {next.title}, {next.time}, {next.meta}, {next.logline}, optional art layer with src "{next.backdrop}" or "{next.poster}".
- tonight (8-16s, usually 8s or 12s, full-screen): Evening schedule board. Use {words.tonight}, {channel.name}, and 3-4 stacked rows using {list[0].time}, {list[0].title}, {list[0].meta} through {list[3].time}, {list[3].title}, {list[3].meta}.
- break (4-8s, usually 8s, full-screen): Outgoing intermission bumper before commercials. Use {words.break} and {channel.name}.
- back (4-8s, usually 8s, full-screen): Return-from-break bumper resuming the programme. Use {words.back}, {channel.name}, {now.title}, optional {now.meta}.
- offair (8-16s, usually 8s or 12s, full-screen): Sign-off / closedown slate. Use {words.offair}, {offair.message}, {channel.name}.
- welcome (8-12s, usually 8s, full-screen): Personal tune-in greeting card. Use {viewer.greeting} or {viewer.name}, {next.title}, {next.reason}, {channel.name}.
- lowerthird (8-12s, usually 8s, TRANSPARENT OVERLAY over live video): NO full-screen background rect and post MUST be []. Anchor a compact banner in lower safe area (x:120..1400, y:760..972) with a translucent dark/brand plate (#080b12@0.82) + accent rule, showing {words.now}, {now.title}, optional {now.meta} or {now.logline}, and "{words.upNext} {next.time} · {next.title}", fading/sliding in (0..0.6s) and out (dur-0.6..dur).
- bug (8s, TRANSPARENT OVERLAY over live video): NO full-screen background rect and post MUST be []. Subtle corner watermark group (opacity 0.28..0.45) in safe corner (e.g. x:1440, y:872, maxWidth:288) with {channel.name} and a soft dark shadow copy behind it for legibility on bright video.

Layer types: %s.
IMPORTANT: There is NO "line" layer type. Draw rules, dividers and hairlines as a thin rect (w, h:2..4, fill) or a stroked path (path:"M x1 y1 L x2 y2", stroke, strokeWidth).
Supported layer fields (every layer requires unique id and human name):
- Transform & timing: x, y, w, h, scale, scaleX, scaleY, rotate, opacity, start, end, repeat (1..200), hidden, locked, blend (normal|multiply|screen|overlay), blur (0..64), glow (0..64), clip (SVG path), reveal ({wipe:ltr|rtl,from,to,out?,ease?}), behave.
- Paint: fill, stroke, strokeWidth, join (miter|round|bevel), cap (butt|round|square), fillRule (nonzero|evenodd). Colours are #RRGGBB, #RRGGBB@alpha (0..1), tokens ({bg},{accent},{text},{glow},{accent}@0.35), or gradients {linear:[x1,y1,x2,y2],stops:[[0,color],[1,color]]} / {radial:[cx,cy,r0,r1],stops:[...]}.
- Type-specific: group (layers:[...]); path (path:"M ..."); blob (x,y,w,h,seed,fill); text (text,font,size,weight,track,align:left|center|right,maxWidth,fit:shrink|wrap|clip,maxLines — align defaults to "left" and x is the anchor point, so centred text MUST set align:"center" with x at the centre such as x:960); image/art/logo (src,w,h,fit:cover|contain,tint,tintBlend:normal|multiply|screen|overlay|silhouette — use art for {next.poster}/{next.backdrop}, image/logo only for supplied 64-hex Library digests or {brand.logo}); gradient (w,h,fill); grid (x,y,w,h,stroke,strokeWidth); particles (x,y,w,h,count:1..500,seed,fill); noise (x,y,w,h,seed,opacity).
- Animation: scalars or {keys:[[seconds,value,easing?],...]} with times ascending in 0..duration.
Easings: %s.
Behaviours: %s (as behave:[{<kind>:{amplitude?,speed?,start?,step?,seed?}}]).
Post effects: %s (as post:[{type,amount:0.04..0.25,seed?}] on full-screen roles only; [] on lowerthird and bug).
Bundled fonts: %s (set brand.type.display and brand.type.body to bundled font ids, and bind text font to "{type.display}" or "{type.body}").

1920x1080 broadcast composition rules:
1. Grid, safe areas & centring: Bleed backgrounds, gradients, grids and particles across 0..1920 x 0..1080, but keep all text, cards, logos, bugs and lower thirds strictly inside title-safe margins x:160..1760, y:108..972. text.align defaults to "left": for left-aligned copy span the safe area from x:160..220 with maxWidth:1480..1600; for horizontally centred hero/bumper copy you MUST set align:"center" with x at the centre (x:960 for full canvas, or card x+w/2) and maxWidth:1200..1600 — NEVER set x:960 without align:"center".
2. Typography & baseline coordinates: text.y is the ALPHABETIC BASELINE (a size:96 headline at y:420 extends upward to ~330). Space consecutive text baselines by at least 1.25 * size. Always set maxWidth and fit:"shrink" (or fit:"wrap" with maxLines:2 for loglines) so long live titles never clip. Type scale: ident/hero display 88-128, card title ({next.title}) 60-84, schedule/section title 38-48, uppercase kicker/time/meta 24-32 with track:2..6.
3. Contrast & scrims: brand.palette.text vs brand.palette.background MUST have >= 4.5:1 contrast. Whenever text sits over {next.backdrop}, {next.poster}, bright gradients or grids, insert a dark scrim rect or gradient ({bg}@0.88 to {bg}@0.35) directly behind the textplane.
4. Three-plane depth & live-slot ordering: Build full-screen scenes in three clear layers from back to front: (a) Background: full-bleed {bg} rect + atmospheric radial/linear glow ({glow}@0.4, {accent}@0.25, blend:"screen"); (b) Midground: era/mood geometry (perspective/architectural grid, drifting ellipses/blobs/particles, framed card panel, crisp thin-rect accent rules); (c) Foreground: live-slot art/text layers ({next.title}, {now.title}, {list[0].title}..{list[3].title}) last in normal blend so playout can pre-bake procedural backdrops.
5. Motion & poster frame: Choreograph staggered entrances over 0..0.9s (opacity + 24-40px slide with "out" or "back", or reveal wipe), keep a living hold across document.poster (set poster to duration*0.5, NEVER 0 when layers fade in from 0), and fade/slide out over the last 0.6s on bumpers and overlays.
6. Brand tokens, controls & textures: Use {bg}, {accent}, {text}, {glow}, {type.display}, {type.body}, {energy}, {words.tagline}, and define 2-4 grouped controls (referenced as {control.<id>} with matching kind). Match brand.textures and post effects to the era/mood (e.g. scanlines+vignette for 80s/CRT, warm grain+vignette for cinema/noir, clean subtle vignette for modern/kids). Never invent programme titles or 64-hex asset digests.

Worked example 1 (full-screen upnext document):
{"v":1,"moment":"upnext","duration":8,"poster":4,"slots":["next.logline","next.meta","next.poster","next.time","next.title"],"layers":[{"id":"bg","name":"Background","type":"rect","x":0,"y":0,"w":1920,"h":1080,"fill":"{bg}"},{"id":"halo","name":"Ambient glow","type":"ellipse","x":{"keys":[[0,980],[8,1080,"inOut"]]},"y":-180,"w":1200,"h":1200,"fill":{"radial":[600,600,0,600],"stops":[[0,"{accent}@0.38"],[1,"{bg}@0"]]},"blend":"screen"},{"id":"grid","name":"Architectural grid","type":"grid","x":0,"y":0,"w":1920,"h":1080,"stroke":"{glow}@0.22","strokeWidth":1},{"id":"card","name":"Info panel","type":"rect","x":160,"y":210,"w":1060,"h":660,"fill":"{bg}@0.82","stroke":"{accent}@0.35","strokeWidth":2},{"id":"rule","name":"Accent bar","type":"rect","x":160,"y":210,"w":{"keys":[[0,0],[0.7,1060,"out"]]},"h":6,"fill":"{accent}"},{"id":"poster-frame","name":"Poster art","type":"art","src":"{next.poster}","x":1280,"y":210,"w":440,"h":660,"fit":"cover","opacity":{"keys":[[0,0],[0.6,1,"out"]]}},{"id":"kicker","name":"Up next kicker","type":"text","text":"{words.upNext} · {next.time}","x":210,"y":310,"font":"{type.body}","size":28,"weight":700,"track":4,"fill":"{accent}","maxWidth":960,"fit":"shrink","opacity":{"keys":[[0,0],[0.45,1,"out"]]}},{"id":"title","name":"Programme title","type":"text","text":"{next.title}","x":210,"y":{"keys":[[0.1,448],[0.7,420,"out"]]},"font":"{type.display}","size":76,"weight":700,"fill":"{text}","maxWidth":960,"fit":"shrink","opacity":{"keys":[[0,0],[0.1,0],[0.7,1,"out"]]}},{"id":"meta","name":"Metadata","type":"text","text":"{next.meta}","x":210,"y":492,"font":"{type.body}","size":30,"fill":"{text}@0.78","maxWidth":960,"fit":"shrink","opacity":{"keys":[[0,0],[0.25,0],[0.8,1,"out"]]}},{"id":"logline","name":"Logline","type":"text","text":"{next.logline}","x":210,"y":576,"font":"{type.body}","size":32,"fill":"{text}@0.9","maxWidth":940,"fit":"wrap","maxLines":3,"opacity":{"keys":[[0,0],[0.35,0],[0.9,1,"out"]]}}],"post":[{"type":"vignette","amount":0.18},{"type":"grain","amount":0.08}]}

Worked example 2 (transparent lowerthird overlay document):
{"v":1,"moment":"lowerthird","duration":8,"poster":4,"slots":["next.time","next.title","now.title"],"layers":[{"id":"lt-wrap","name":"Lower third","type":"group","opacity":{"keys":[[0,0],[0.5,1,"out"],[7.4,1],[8,0,"in"]]},"layers":[{"id":"lt-plate","name":"Banner plate","type":"rect","x":160,"y":800,"w":1160,"h":152,"fill":{"linear":[0,0,1160,0],"stops":[[0,"#080b12@0.88"],[1,"#080b12@0.58"]]},"stroke":"{accent}@0.4","strokeWidth":2},{"id":"lt-accent","name":"Left accent","type":"rect","x":160,"y":800,"w":8,"h":152,"fill":"{accent}"},{"id":"lt-kicker","name":"Now playing label","type":"text","text":"{words.now}","x":196,"y":844,"font":"{type.body}","size":24,"weight":700,"track":3,"fill":"{accent}","maxWidth":1080,"fit":"shrink"},{"id":"lt-now","name":"Current programme","type":"text","text":"{now.title}","x":196,"y":900,"font":"{type.display}","size":50,"weight":700,"fill":"#f8fafc","maxWidth":1080,"fit":"shrink"},{"id":"lt-next","name":"Next programme","type":"text","text":"{words.upNext} {next.time} · {next.title}","x":196,"y":936,"font":"{type.body}","size":26,"fill":"#cbd5e1","maxWidth":1080,"fit":"shrink"}]}],"post":[]}`,
		len(Roles),
		strings.Join(Roles, ", "),
		len(Roles),
		strings.Join(scene.SupportedSlots, ", "),
		strings.Join(scene.SupportedWordTokens, ", "),
		strings.Join(scene.SupportedBrandTokens, ", "),
		strings.Join(scene.LayerTypes, ", "),
		strings.Join(scene.Easings, ", "),
		strings.Join(scene.BehaviorKinds, ", "),
		strings.Join(scene.PostEffects, ", "),
		strings.Join(BundledFonts, ", "),
	)
}

func defaultBaseLook() Look {
	l := familyLook("clean", "New style")
	l.Roles["welcome"] = RoleConfig{Duration: 8}
	l.Scenes = append(l.Scenes, defaultWelcomeScene("clean-welcome"))
	return l
}

func defaultWelcomeScene(id string) StyleScene {
	return StyleScene{
		ID:      id,
		Role:    "welcome",
		Name:    "Welcome card",
		Enabled: true,
		Weight:  1,
		Document: scene.Document{
			V:        1,
			Moment:   "welcome",
			Duration: 8,
			Poster:   4,
			Slots:    []string{"channel.name", "next.reason", "next.title", "viewer.name"},
			Layers: []scene.Layer{
				{ID: "bg", Name: "Background", Type: "rect", X: 0.0, Y: 0.0, W: 1920.0, H: 1080.0, Fill: "{bg}"},
				{ID: "glow", Name: "Ambient glow", Type: "ellipse", X: 360.0, Y: -160.0, W: 1200.0, H: 1200.0, Fill: map[string]any{"radial": []any{600.0, 600.0, 0.0, 600.0}, "stops": []any{[]any{0.0, "{accent}@0.35"}, []any{1.0, "{bg}@0"}}}, Blend: "screen"},
				{ID: "card", Name: "Greeting panel", Type: "rect", X: 260.0, Y: 280.0, W: 1400.0, H: 520.0, Fill: "{bg}@0.8", Stroke: "{accent}@0.35", StrokeWidth: 2.0},
				{ID: "rule", Name: "Top accent", Type: "rect", X: 260.0, Y: 280.0, W: 1400.0, H: 6.0, Fill: "{accent}"},
				{ID: "kicker", Name: "Station label", Type: "text", Text: "{channel.name}", X: 960.0, Y: 376.0, Font: "{type.body}", Size: 28.0, Track: 4.0, Align: "center", Fill: "{accent}", MaxWidth: 1240, Fit: "shrink"},
				{ID: "greeting", Name: "Viewer greeting", Type: "text", Text: "Welcome back, {viewer.name}", X: 960.0, Y: 486.0, Font: "{type.display}", Size: 84.0, Align: "center", Fill: "{text}", MaxWidth: 1240, Fit: "shrink"},
				{ID: "programme", Name: "Next programme", Type: "text", Text: "{next.title}", X: 960.0, Y: 604.0, Font: "{type.display}", Size: 48.0, Align: "center", Fill: "{text}", MaxWidth: 1240, Fit: "shrink"},
				{ID: "reason", Name: "Selection note", Type: "text", Text: "{next.reason}", X: 960.0, Y: 676.0, Font: "{type.body}", Size: 30.0, Align: "center", Fill: "{text}@0.78", MaxWidth: 1240, Fit: "shrink"},
			},
			Post: []scene.Effect{{Type: "vignette", Amount: 0.16}},
		},
		Sounds: []SceneSound{},
	}
}

func (s *Service) Generate(ctx context.Context, prompt, summary string, base Look) (Look, error) {
	return s.GenerateScene(ctx, prompt, summary, base, "")
}

func (s *Service) GenerateScene(ctx context.Context, prompt, summary string, base Look, sceneID string) (Look, error) {
	if s.o.Chat == nil || !s.o.Chat.Enabled() {
		return Look{}, fmt.Errorf("connect a language model in Integrations to describe a style")
	}
	if len(strings.TrimSpace(prompt)) == 0 || len(prompt) > 2000 {
		return Look{}, fmt.Errorf("prompt must be between 1 and 2000 characters")
	}
	if len(base.Scenes) == 0 {
		base = defaultBaseLook()
	}
	if sceneID != "" {
		found := false
		for _, sc := range base.Scenes {
			if sc.ID == sceneID {
				found = true
			}
		}
		if !found {
			return Look{}, fmt.Errorf("scene does not exist")
		}
	}
	guide := "Return a complete Style JSON object, without prose or markdown. Do not return palette-only edits. " + StyleDraftGuide
	if identEx, e := families.Get("clean", "ident"); e == nil {
		b, _ := json.Marshal(identEx)
		guide += "\nWorked example 3 (full-screen ident document): " + string(b)
	}
	assets, e := s.Assets(ctx)
	if e != nil {
		return Look{}, e
	}
	ab, _ := json.Marshal(assets)
	b, _ := json.Marshal(base)
	msgs := []llm.Message{{Role: "system", Content: guide}, {Role: "user", Content: fmt.Sprintf("Request: %s\nChannel summary: %s\nLibrary: %s\nBase style: %s\nOnly change scene ID (empty means whole style): %s", prompt, summary, ab, b, sceneID)}}
	var err error
	var collected []StyleScene
	for attempt := 0; attempt < 3; attempt++ {
		raw, e := s.o.Chat.Chat(ctx, msgs, true)
		if e != nil {
			return Look{}, e
		}
		var l Look
		l, err = ParseLookDraft([]byte(strings.TrimSpace(raw)))
		if err == nil {
			l.ID = base.ID
			l.ChannelIDs = base.ChannelIDs
			if sceneID != "" {
				var replacement *StyleScene
				for i := range l.Scenes {
					if l.Scenes[i].ID == sceneID {
						replacement = &l.Scenes[i]
					}
				}
				if replacement == nil && len(l.Scenes) == 1 {
					only := l.Scenes[0]
					only.ID = sceneID
					for _, orig := range base.Scenes {
						if orig.ID == sceneID {
							only.Role = orig.Role
							only.Document.Moment = orig.Role
						}
					}
					replacement = &only
				}
				if replacement == nil {
					err = fmt.Errorf("include the requested scene")
				} else {
					next := base
					next.Roles = map[string]RoleConfig{}
					for k, v := range base.Roles {
						next.Roles[k] = v
					}
					next.Roles[replacement.Role] = RoleConfig{Duration: replacement.Document.Duration}
					next.Scenes = append([]StyleScene(nil), base.Scenes...)
					for i := range next.Scenes {
						if next.Scenes[i].ID == sceneID {
							next.Scenes[i] = *replacement
						}
					}
					l = next
				}
			} else if len(collected) > 0 {
				// Preserve already-validated scenes from earlier attempts in this
				// turn when a repair response supplies only the missing roles.
				l = mergeMissingRoleScenes(l, collected)
			}
			if err == nil {
				l, err = s.Draft(ctx, l)
				if err == nil {
					if sceneID == "" {
						collected = mergeMissingRoleScenes(Look{Roles: l.Roles, Scenes: l.Scenes}, collected).Scenes
						if missing := MissingRoles(l); len(missing) > 0 {
							err = fmt.Errorf("style is missing required roles (%s); include at least one enabled, non-empty scene for every role", strings.Join(missing, ", "))
						}
					}
					if err == nil {
						return l, nil
					}
				}
			}
		}
		msgs = append(msgs, llm.Message{Role: "assistant", Content: raw}, llm.Message{Role: "user", Content: "Repair the complete proposal: " + err.Error()})
	}
	return Look{}, fmt.Errorf("model returned an invalid style: %w", err)
}

func mergeMissingRoleScenes(dst Look, prior []StyleScene) Look {
	if dst.Roles == nil {
		dst.Roles = map[string]RoleConfig{}
	}
	missing := map[string]bool{}
	for _, r := range MissingRoles(dst) {
		missing[r] = true
	}
	if len(missing) == 0 {
		return dst
	}
	usedIDs := map[string]bool{}
	for _, sc := range dst.Scenes {
		usedIDs[sc.ID] = true
	}
	for _, sc := range prior {
		if !missing[sc.Role] || !sc.Enabled || len(sc.Document.Layers) == 0 {
			continue
		}
		cp := sc
		cp.ID = uniqueID(cp.ID, usedIDs)
		dur := cp.Document.Duration
		if rc, ok := dst.Roles[cp.Role]; ok && rc.Duration >= 4 {
			dur = rc.Duration
			cp.Document.Duration = dur
			if cp.Document.Poster >= dur {
				cp.Document.Poster = dur / 2
			}
		} else {
			dst.Roles[cp.Role] = RoleConfig{Duration: dur}
		}
		dst.Scenes = append(dst.Scenes, cp)
		delete(missing, cp.Role)
	}
	return dst
}

func (s *Service) Template(ctx context.Context, prompt string, base Look) (Look, error) {
	return s.Generate(ctx, prompt, "", base)
}
