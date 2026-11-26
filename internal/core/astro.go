package core

import (
	"math"
	"time"
)

// Astronomy for computed holidays (holidays.go): true new and full moons
// (Meeus, Astronomical Algorithms ch. 49) and the equinoxes and solar terms
// the lunisolar calendars hang on (ch. 27 and a low-precision solar
// longitude). Accuracy is minutes for the moon and well under a day for the
// sun, which is ample: holiday windows span days.

const (
	jdUnixEpoch = 2440587.5
	degToRad    = math.Pi / 180
)

func jdToTime(jd float64) time.Time {
	sec := (jd - jdUnixEpoch) * 86400
	return time.Unix(int64(math.Floor(sec)), 0).UTC()
}

func timeToJD(t time.Time) float64 {
	return float64(t.Unix())/86400 + jdUnixEpoch
}

// moonPhase returns the instant (UTC) of lunation k's phase: k integer for a
// new moon, k+0.5 for a full moon. k = 0 is the new moon of 2000-01-06.
func moonPhase(k float64) time.Time {
	t := k / 1236.85
	t2, t3, t4 := t*t, t*t*t, t*t*t*t
	jde := 2451550.09766 + 29.530588861*k + 0.00015437*t2 - 0.000000150*t3 + 0.00000000073*t4
	e := 1 - 0.002516*t - 0.0000074*t2
	m := (2.5534 + 29.10535670*k - 0.0000014*t2 - 0.00000011*t3) * degToRad
	mp := (201.5643 + 385.81693528*k + 0.0107582*t2 + 0.00001238*t3 - 0.000000058*t4) * degToRad
	f := (160.7108 + 390.67050284*k - 0.0016118*t2 - 0.00000227*t3 + 0.000000011*t4) * degToRad
	om := (124.7746 - 1.56375588*k + 0.0020672*t2 + 0.00000215*t3) * degToRad
	sin := math.Sin
	var c float64
	if k == math.Floor(k) { // new moon
		c = -0.40720*sin(mp) + 0.17241*e*sin(m) + 0.01608*sin(2*mp) + 0.01039*sin(2*f) +
			0.00739*e*sin(mp-m) - 0.00514*e*sin(mp+m) + 0.00208*e*e*sin(2*m) - 0.00111*sin(mp-2*f) -
			0.00057*sin(mp+2*f) + 0.00056*e*sin(2*mp+m) - 0.00042*sin(3*mp) + 0.00042*e*sin(m+2*f) +
			0.00038*e*sin(m-2*f) - 0.00024*e*sin(2*mp-m) - 0.00017*sin(om) - 0.00007*sin(mp+2*m)
	} else { // full moon
		c = -0.40614*sin(mp) + 0.17302*e*sin(m) + 0.01614*sin(2*mp) + 0.01043*sin(2*f) +
			0.00734*e*sin(mp-m) - 0.00515*e*sin(mp+m) + 0.00209*e*e*sin(2*m) - 0.00111*sin(mp-2*f) -
			0.00057*sin(mp+2*f) + 0.00056*e*sin(2*mp+m) - 0.00042*sin(3*mp) + 0.00042*e*sin(m+2*f) +
			0.00038*e*sin(m-2*f) - 0.00024*e*sin(2*mp-m) - 0.00017*sin(om) - 0.00007*sin(mp+2*m)
	}
	// ΔT is about a minute this century; the result is treated as UT.
	return jdToTime(jde + c)
}

// lunationNear returns the lunation number of the new moon nearest t.
func lunationNear(t time.Time) float64 {
	return math.Round((timeToJD(t) - 2451550.09766) / 29.530588861)
}

// newMoonOnOrBefore returns the last new moon at or before t.
func newMoonOnOrBefore(t time.Time) time.Time {
	k := lunationNear(t)
	for {
		nm := moonPhase(k)
		if !nm.After(t) {
			return nm
		}
		k--
	}
}

// fullMoonsBetween lists full moons in [from, to].
func fullMoonsBetween(from, to time.Time) []time.Time {
	var out []time.Time
	for k := lunationNear(from) - 1; ; k++ {
		fm := moonPhase(k + 0.5)
		if fm.After(to) {
			return out
		}
		if !fm.Before(from) {
			out = append(out, fm)
		}
	}
}

// newMoonsBetween lists new moons in [from, to].
func newMoonsBetween(from, to time.Time) []time.Time {
	var out []time.Time
	for k := lunationNear(from) - 1; ; k++ {
		nm := moonPhase(k)
		if nm.After(to) {
			return out
		}
		if !nm.Before(from) {
			out = append(out, nm)
		}
	}
}

// sunLongitude is the Sun's apparent ecliptic longitude in degrees [0,360).
func sunLongitude(t time.Time) float64 {
	n := timeToJD(t) - 2451545.0
	l := math.Mod(280.460+0.9856474*n, 360)
	g := math.Mod(357.528+0.9856003*n, 360) * degToRad
	lambda := l + 1.915*math.Sin(g) + 0.020*math.Sin(2*g)
	lambda = math.Mod(lambda, 360)
	if lambda < 0 {
		lambda += 360
	}
	return lambda
}

// solarTerm returns the instant in year y (UTC) when the Sun reaches
// longitude deg, found by bisection around its mean date.
func solarTerm(y int, deg float64) time.Time {
	// Mean day of the year for the longitude (the March equinox is about
	// day 79), then bisect a window around it; the true date is within a
	// few days of the mean.
	doy := math.Mod(79.3+math.Mod(deg+360, 360)/360*365.2422, 365.2422)
	mean := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(doy * 24 * float64(time.Hour)))
	lo, hi := mean.AddDate(0, 0, -9), mean.AddDate(0, 0, 9)
	diff := func(t time.Time) float64 {
		d := sunLongitude(t) - deg
		for d > 180 {
			d -= 360
		}
		for d < -180 {
			d += 360
		}
		return d
	}
	for i := 0; i < 40; i++ {
		mid := lo.Add(hi.Sub(lo) / 2)
		if diff(mid) < 0 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo
}
