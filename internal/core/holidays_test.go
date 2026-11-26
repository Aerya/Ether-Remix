package core

import (
	"testing"
	"time"
)

func TestHolidayDates(t *testing.T) {
	cases := []struct {
		name string
		got  civilDate
		want string
		tol  int // days of tolerance (tabular Islamic calendar)
	}{
		{"easter 2024", westernEaster(2024), "2024-03-31", 0},
		{"easter 2025", westernEaster(2025), "2025-04-20", 0},
		{"easter 2026", westernEaster(2026), "2026-04-05", 0},
		{"orthodox easter 2024", orthodoxEaster(2024), "2024-05-05", 0},
		{"orthodox easter 2025", orthodoxEaster(2025), "2025-04-20", 0},
		{"orthodox easter 2026", orthodoxEaster(2026), "2026-04-12", 0},
		{"rosh hashanah 2025", roshHashanah(2025), "2025-09-23", 0},
		{"rosh hashanah 2026", roshHashanah(2026), "2026-09-12", 0},
		{"passover 2025", roshHashanah(2025).add(-163), "2025-04-13", 0},
		{"passover 2026", roshHashanah(2026).add(-163), "2026-04-02", 0},
		{"hanukkah 2025", hanukkah(2025), "2025-12-15", 0},
		{"hanukkah 2026", hanukkah(2026), "2026-12-05", 0},
		{"ramadan 1446", islamicDate(1446, 9, 1), "2025-03-01", 1},
		{"eid al-fitr 1446", islamicDate(1446, 10, 1), "2025-03-30", 1},
		{"eid al-adha 1446", islamicDate(1446, 12, 10), "2025-06-06", 1},
		{"lunar new year 2023", lunarNewYear(2023), "2023-01-22", 0},
		{"lunar new year 2024", lunarNewYear(2024), "2024-02-10", 0},
		{"lunar new year 2025", lunarNewYear(2025), "2025-01-29", 0},
		{"lunar new year 2026", lunarNewYear(2026), "2026-02-17", 0},
		{"lunar new year 2027", lunarNewYear(2027), "2027-02-06", 0},
		{"mid-autumn 2023", midAutumn(2023), "2023-09-29", 0},
		{"mid-autumn 2024", midAutumn(2024), "2024-09-17", 0},
		{"mid-autumn 2025", midAutumn(2025), "2025-10-06", 0},
		{"mid-autumn 2026", midAutumn(2026), "2026-09-25", 0},
		{"diwali 2024", diwali(2024), "2024-11-01", 1},
		{"diwali 2025", diwali(2025), "2025-10-21", 1},
		{"diwali 2026", diwali(2026), "2026-11-09", 1},
		{"holi 2024", holi(2024), "2024-03-25", 1},
		{"holi 2025", holi(2025), "2025-03-14", 1},
		{"holi 2026", holi(2026), "2026-03-03", 1},
		{"vesak 2025", vesak(2025), "2025-05-12", 1},
		{"thanksgiving us 2025", nthWeekday(2025, 11, time.Thursday, 4), "2025-11-27", 0},
		{"thanksgiving ca 2025", nthWeekday(2025, 10, time.Monday, 2), "2025-10-13", 0},
	}
	for _, c := range cases {
		want, err := time.Parse("2006-01-02", c.want)
		if err != nil {
			t.Fatal(err)
		}
		diff := int(c.got.time().Sub(want).Hours() / 24)
		if diff < -c.tol || diff > c.tol {
			t.Errorf("%s = %s, want %s (±%d)", c.name, c.got, c.want, c.tol)
		}
	}
}

func TestHolidayActive(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		id   string
		now  string
		want bool
	}{
		{HolidayChristmas, "2026-12-20 21:00", true},
		{HolidayChristmas, "2026-12-27 21:00", false},
		{HolidayNewYear, "2027-01-01 10:00", true},
		{HolidayNewYear, "2026-12-30 10:00", true},
		{HolidayNewYear, "2027-01-02 10:00", false},
		{HolidayHanukkah, "2025-12-18 19:00", true},
		{HolidayRamadan, "2025-03-15 20:00", true},
		{HolidayRamadan, "2025-05-15 20:00", false},
		{HolidayLunarNewYear, "2026-02-18 12:00", true},
		{HolidayHalloween, "2026-11-05 12:00", false},
	}
	for _, c := range cases {
		if got := HolidayActive(c.id, at(c.now)); got != c.want {
			t.Errorf("HolidayActive(%s, %s) = %v, want %v", c.id, c.now, got, c.want)
		}
	}
	for _, h := range Holidays {
		for y := 2020; y <= 2060; y++ {
			if len(HolidayWindows(h.ID, y)) == 0 && h.ID != HolidayRamadan && h.ID != HolidayEidAlFitr && h.ID != HolidayEidAlAdha {
				t.Errorf("%s has no window in %d", h.ID, y)
			}
		}
	}
}
