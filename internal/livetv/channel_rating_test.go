package livetv

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/contentrating"
)

func TestChannelRatingPrecedence(t *testing.T) {
	c := Channel{ID: "news", Kind: "iptv", SourceID: "provider", Group: "News"}
	guides := indexGroupRatings([]OwnerGroup{
		{Hidden: true, All: true, UnratedRating: "R"},
		{Groups: []OwnerGroupSource{{SourceID: "provider", Name: "News"}}, UnratedRating: "PG"},
		{Channels: []string{"news"}, UnratedRating: "G"},
	})
	for _, tc := range []struct {
		name, programme, channel, guide, source, want string
		adult                                         bool
	}{
		{name: "programme", programme: "TV-MA", channel: "R", guide: "PG", source: "G", want: "TV-MA"},
		{name: "channel", channel: "R", guide: "PG", source: "G", want: "R"},
		{name: "guide", guide: guides.rating(c), source: "G", want: "PG"},
		{name: "source", source: "G", want: "G"},
		{name: "unset"},
		{name: "adult", guide: "G", source: "G", adult: true},
		{name: "adult explicit", programme: "TV-MA", guide: "G", source: "G", adult: true, want: "TV-MA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := c
			ch.Rating = tc.channel
			got := resolveChannelRating(ch, &Programme{Item: Item{Rating: tc.programme}}, tc.guide, tc.source, tc.adult)
			if got != tc.want {
				t.Fatalf("rating = %q, want %q", got, tc.want)
			}
			if got == "" && !contentrating.Exceeds(got, "R") {
				t.Fatal("unrated must fail closed")
			}
		})
	}
}
