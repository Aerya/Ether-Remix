package core

import (
	"math"
	"time"
)

// Holidays a row schedule can name (Schedule.Holidays). Every window is
// computed for the year asked, never read from a table that expires:
// Easter by computus, Jewish dates from the arithmetic Hebrew calendar,
// Islamic dates from the tabular calendar (a day either side of local
// sighting; the windows already span days), and the lunisolar holidays
// from true new and full moons (astro.go).

// HolidayInfo describes one holiday for the console picker.
type HolidayInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Group is the picker section: seasons, christian, jewish, islamic,
	// asian or more.
	Group string `json:"group"`
	// When describes the window in plain words.
	When string `json:"when"`
}

// DateWindow is an inclusive range of local calendar days.
type DateWindow struct {
	From civilDate
	To   civilDate
}

type civilDate struct{ y, m, d int }

func civil(t time.Time) civilDate {
	y, m, d := t.Date()
	return civilDate{y, int(m), d}
}

func (c civilDate) time() time.Time {
	return time.Date(c.y, time.Month(c.m), c.d, 0, 0, 0, 0, time.UTC)
}
func (c civilDate) add(days int) civilDate { return civil(c.time().AddDate(0, 0, days)) }
func (c civilDate) before(o civilDate) bool {
	if c.y != o.y {
		return c.y < o.y
	}
	if c.m != o.m {
		return c.m < o.m
	}
	return c.d < o.d
}

// String formats the day as YYYY-MM-DD.
func (c civilDate) String() string { return c.time().Format("2006-01-02") }

func (w DateWindow) contains(c civilDate) bool { return !c.before(w.From) && !w.To.before(c) }

// Holiday ids.
const (
	HolidayChristmas      = "christmas"
	HolidayNewYear        = "new-year"
	HolidayHalloween      = "halloween"
	HolidayValentines     = "valentines"
	HolidayThanksgivingUS = "thanksgiving-us"
	HolidayThanksgivingCA = "thanksgiving-ca"
	HolidayEaster         = "easter"
	HolidayEasterOrthodox = "easter-orthodox"
	HolidayCarnival       = "carnival"
	HolidayHanukkah       = "hanukkah"
	HolidayPassover       = "passover"
	HolidayRoshHashanah   = "rosh-hashanah"
	HolidayRamadan        = "ramadan"
	HolidayEidAlFitr      = "eid-al-fitr"
	HolidayEidAlAdha      = "eid-al-adha"
	HolidayLunarNewYear   = "lunar-new-year"
	HolidayMidAutumn      = "mid-autumn"
	HolidayDiwali         = "diwali"
	HolidayHoli           = "holi"
	HolidayVesak          = "vesak"
	HolidayDiaDeMuertos   = "dia-de-muertos"
	HolidayNowruz         = "nowruz"
)

// Holidays is the picker list, in picker order.
var Holidays = []HolidayInfo{
	{HolidayChristmas, "Christmas season", "seasons", "Dec 1–26"},
	{HolidayNewYear, "New Year", "seasons", "Dec 26–Jan 1"},
	{HolidayHalloween, "Halloween", "seasons", "October"},
	{HolidayValentines, "Valentine's Day", "seasons", "Feb 7–14"},
	{HolidayThanksgivingUS, "Thanksgiving (US)", "seasons", "The week of the fourth Thursday of November"},
	{HolidayThanksgivingCA, "Thanksgiving (Canada)", "seasons", "The week up to the second Monday of October"},
	{HolidayEaster, "Easter", "christian", "Palm Sunday to Easter Monday"},
	{HolidayEasterOrthodox, "Orthodox Easter", "christian", "Palm Sunday to Easter Monday"},
	{HolidayCarnival, "Carnival", "christian", "The five days to Shrove Tuesday"},
	{HolidayHanukkah, "Hanukkah", "jewish", "The eight days"},
	{HolidayPassover, "Passover", "jewish", "The eight days"},
	{HolidayRoshHashanah, "Rosh Hashanah", "jewish", "The eve and both days"},
	{HolidayRamadan, "Ramadan", "islamic", "The whole month"},
	{HolidayEidAlFitr, "Eid al-Fitr", "islamic", "The eve and three days"},
	{HolidayEidAlAdha, "Eid al-Adha", "islamic", "The eve and four days"},
	{HolidayLunarNewYear, "Lunar New Year", "asian", "Two days before to a week after"},
	{HolidayMidAutumn, "Mid-Autumn Festival", "asian", "The festival and a day either side"},
	{HolidayDiwali, "Diwali", "asian", "Five days around Lakshmi Puja"},
	{HolidayHoli, "Holi", "asian", "Holika Dahan and Holi"},
	{HolidayVesak, "Vesak", "asian", "The full moon and a day either side"},
	{HolidayDiaDeMuertos, "Día de los Muertos", "more", "Oct 31–Nov 2"},
	{HolidayNowruz, "Nowruz", "more", "Mar 20–Apr 2"},
}

// ValidHoliday reports whether id names a holiday.
func ValidHoliday(id string) bool {
	for _, h := range Holidays {
		if h.ID == id {
			return true
		}
	}
	return false
}

// HolidayWindows returns the holiday's windows that start in year y.
// Windows can run into the next year (New Year).
func HolidayWindows(id string, y int) []DateWindow {
	fixed := func(m1, d1, m2, d2 int) []DateWindow {
		from := civilDate{y, m1, d1}
		to := civilDate{y, m2, d2}
		if to.before(from) {
			to.y++
		}
		return []DateWindow{{from, to}}
	}
	around := func(c civilDate, before, after int) []DateWindow {
		return []DateWindow{{c.add(-before), c.add(after)}}
	}
	switch id {
	case HolidayChristmas:
		return fixed(12, 1, 12, 26)
	case HolidayNewYear:
		return fixed(12, 26, 1, 1)
	case HolidayHalloween:
		return fixed(10, 1, 10, 31)
	case HolidayValentines:
		return fixed(2, 7, 2, 14)
	case HolidayDiaDeMuertos:
		return fixed(10, 31, 11, 2)
	case HolidayNowruz:
		return fixed(3, 20, 4, 2)
	case HolidayThanksgivingUS:
		return around(nthWeekday(y, 11, time.Thursday, 4), 7, 3)
	case HolidayThanksgivingCA:
		return around(nthWeekday(y, 10, time.Monday, 2), 6, 0)
	case HolidayEaster:
		return around(westernEaster(y), 7, 1)
	case HolidayEasterOrthodox:
		return around(orthodoxEaster(y), 7, 1)
	case HolidayCarnival:
		return around(westernEaster(y).add(-47), 4, 0)
	case HolidayRoshHashanah:
		return around(roshHashanah(y), 1, 1)
	case HolidayPassover:
		// 15 Nisan is always 163 days before the next Rosh Hashanah.
		return around(roshHashanah(y).add(-163), 1, 7)
	case HolidayHanukkah:
		return around(hanukkah(y), 1, 7)
	case HolidayRamadan, HolidayEidAlFitr, HolidayEidAlAdha:
		var out []DateWindow
		for _, h := range islamicYearsIn(y) {
			var w DateWindow
			switch id {
			case HolidayRamadan:
				w = DateWindow{islamicDate(h, 9, 1).add(-1), islamicDate(h, 10, 1)}
			case HolidayEidAlFitr:
				w = DateWindow{islamicDate(h, 10, 1).add(-1), islamicDate(h, 10, 1).add(3)}
			default:
				w = DateWindow{islamicDate(h, 12, 10).add(-1), islamicDate(h, 12, 10).add(4)}
			}
			if w.From.y == y {
				out = append(out, w)
			}
		}
		return out
	case HolidayLunarNewYear:
		return around(lunarNewYear(y), 2, 7)
	case HolidayMidAutumn:
		return around(midAutumn(y), 1, 1)
	case HolidayDiwali:
		return around(diwali(y), 2, 2)
	case HolidayHoli:
		return around(holi(y), 1, 1)
	case HolidayVesak:
		return around(vesak(y), 1, 1)
	}
	return nil
}

// HolidayActive reports whether the local day of now falls in the holiday.
func HolidayActive(id string, now time.Time) bool {
	day := civil(now)
	for _, y := range []int{day.y - 1, day.y} {
		for _, w := range HolidayWindows(id, y) {
			if w.contains(day) {
				return true
			}
		}
	}
	return false
}

func nthWeekday(y, m int, wd time.Weekday, n int) civilDate {
	first := time.Date(y, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
	offset := (int(wd) - int(first.Weekday()) + 7) % 7
	return civil(first.AddDate(0, 0, offset+7*(n-1)))
}

// westernEaster is the Gregorian computus (anonymous algorithm).
func westernEaster(y int) civilDate {
	a := y % 19
	b, c := y/100, y%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return civilDate{y, month, day}
}

// orthodoxEaster is the Julian computus, moved onto the Gregorian calendar.
func orthodoxEaster(y int) civilDate {
	a, b, c := y%4, y%7, y%19
	d := (19*c + 15) % 30
	e := (2*a + 4*b - d + 34) % 7
	month := (d + e + 114) / 31
	day := (d+e+114)%31 + 1
	shift := y/100 - y/400 - 2 // Julian to Gregorian offset (13 days in 1900–2099)
	return civilDate{y, month, day}.add(shift)
}

// ---- Hebrew calendar (Dershowitz & Reingold, arithmetic) ----

func hebrewLeap(h int) bool { return (7*h+1)%19 < 7 }

func hebrewElapsedDays(h int) int {
	monthsElapsed := 235*((h-1)/19) + 12*((h-1)%19) + (7*((h-1)%19)+1)/19
	partsElapsed := 204 + 793*(monthsElapsed%1080)
	hoursElapsed := 5 + 12*monthsElapsed + 793*(monthsElapsed/1080) + partsElapsed/1080
	day := 1 + 29*monthsElapsed + hoursElapsed/24
	parts := 1080*(hoursElapsed%24) + partsElapsed%1080
	if parts >= 19440 || (day%7 == 2 && parts >= 9924 && !hebrewLeap(h)) || (day%7 == 1 && parts >= 16789 && hebrewLeap(h-1)) {
		day++
	}
	if day%7 == 0 || day%7 == 3 || day%7 == 5 {
		day++
	}
	return day
}

// fixedDate converts a Rata Die day number (1 = 0001-01-01) to a civil date.
func fixedDate(rd int) civilDate {
	return civil(time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, rd-1))
}

const hebrewEpochRD = -1373429

// roshHashanahOf returns 1 Tishrei of Hebrew year h.
func roshHashanahOf(h int) civilDate { return fixedDate(hebrewEpochRD + hebrewElapsedDays(h) + 1) }

// roshHashanah returns the Rosh Hashanah that falls in Gregorian year y.
func roshHashanah(y int) civilDate { return roshHashanahOf(y + 3761) }

// hanukkah returns 25 Kislev in Gregorian year y (it starts the Hebrew year
// that began that autumn).
func hanukkah(y int) civilDate {
	h := y + 3761
	yearLen := hebrewElapsedDays(h+1) - hebrewElapsedDays(h)
	heshvan := 29
	if yearLen%10 == 5 { // complete years (355, 385) have a 30-day Heshvan
		heshvan = 30
	}
	return roshHashanahOf(h).add(30 + heshvan + 24)
}

// ---- Islamic calendar (tabular) ----

const islamicEpochRD = 227015

func islamicDate(h, m, d int) civilDate {
	rd := islamicEpochRD - 1 + (h-1)*354 + (3+11*h)/30 + 29*(m-1) + m/2 + d
	return fixedDate(rd)
}

// islamicYearsIn lists the Hijri years that may have dates in Gregorian y.
func islamicYearsIn(y int) []int {
	h := int(math.Floor(float64(y-622) * 33 / 32))
	return []int{h - 1, h, h + 1, h + 2}
}

// ---- lunisolar holidays from true moons ----

var (
	chinaTime = time.FixedZone("China", 8*3600)
	indiaTime = time.FixedZone("India", 5*3600+1800)
)

// chineseMonthStart returns the first day (China time) of the lunar month
// holding the China-time day of t: a month starts on the China day of its
// new moon, so the last new moon before that day ends is the one.
func chineseMonthStart(t time.Time) civilDate {
	day := civil(t.In(chinaTime))
	endOfDay := time.Date(day.y, time.Month(day.m), day.d, 23, 59, 59, 0, chinaTime)
	return civil(newMoonOnOrBefore(endOfDay).In(chinaTime))
}

// lunarNewYear is the first day of the month holding Yushui (the Sun at
// 330°), which is month one.
func lunarNewYear(y int) civilDate { return chineseMonthStart(solarTerm(y, 330)) }

// midAutumn is day 15 of month eight, the month holding the autumn equinox.
func midAutumn(y int) civilDate { return chineseMonthStart(solarTerm(y, 180)).add(14) }

// diwali is the new moon (India time) of Kartika, between mid-October and
// mid-November.
func diwali(y int) civilDate {
	for _, nm := range newMoonsBetween(time.Date(y, 10, 12, 0, 0, 0, 0, time.UTC), time.Date(y, 11, 15, 0, 0, 0, 0, time.UTC)) {
		return civil(nm.In(indiaTime))
	}
	return civilDate{y, 11, 1}
}

// holi is the Phalguna full moon (India time): the latest full moon from
// Feb 25 to Mar 28.
func holi(y int) civilDate {
	moons := fullMoonsBetween(time.Date(y, 2, 25, 0, 0, 0, 0, indiaTime), time.Date(y, 3, 29, 0, 0, 0, 0, indiaTime))
	if len(moons) == 0 {
		return civilDate{y, 3, 15}
	}
	return civil(moons[len(moons)-1].In(indiaTime))
}

// vesak is the first full moon from late April (the Vesakha full moon).
func vesak(y int) civilDate {
	moons := fullMoonsBetween(time.Date(y, 4, 26, 0, 0, 0, 0, time.UTC), time.Date(y, 5, 31, 0, 0, 0, 0, time.UTC))
	if len(moons) == 0 {
		return civilDate{y, 5, 15}
	}
	return civil(moons[0].In(indiaTime))
}
