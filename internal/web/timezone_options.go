package web

import (
	"fmt"
	"time"
)

type timezoneOption struct {
	Value    string
	Label    string
	Selected bool
}

var timezoneNames = []string{
	"UTC",
	"Africa/Accra",
	"Africa/Addis_Ababa",
	"Africa/Algiers",
	"Africa/Cairo",
	"Africa/Casablanca",
	"Africa/Dar_es_Salaam",
	"Africa/Johannesburg",
	"Africa/Lagos",
	"Africa/Nairobi",
	"Africa/Tunis",
	"America/Anchorage",
	"America/Argentina/Buenos_Aires",
	"America/Asuncion",
	"America/Bogota",
	"America/Caracas",
	"America/Chicago",
	"America/Costa_Rica",
	"America/Denver",
	"America/Edmonton",
	"America/El_Salvador",
	"America/Guatemala",
	"America/Halifax",
	"America/Havana",
	"America/Honolulu",
	"America/La_Paz",
	"America/Lima",
	"America/Los_Angeles",
	"America/Managua",
	"America/Mexico_City",
	"America/Montevideo",
	"America/New_York",
	"America/Panama",
	"America/Phoenix",
	"America/Puerto_Rico",
	"America/Santiago",
	"America/Santo_Domingo",
	"America/Sao_Paulo",
	"America/St_Johns",
	"America/Tegucigalpa",
	"America/Toronto",
	"America/Vancouver",
	"America/Winnipeg",
	"Asia/Almaty",
	"Asia/Amman",
	"Asia/Ashgabat",
	"Asia/Baghdad",
	"Asia/Baku",
	"Asia/Bangkok",
	"Asia/Beirut",
	"Asia/Colombo",
	"Asia/Damascus",
	"Asia/Dhaka",
	"Asia/Dubai",
	"Asia/Ho_Chi_Minh",
	"Asia/Hong_Kong",
	"Asia/Irkutsk",
	"Asia/Jakarta",
	"Asia/Jerusalem",
	"Asia/Kabul",
	"Asia/Kamchatka",
	"Asia/Karachi",
	"Asia/Kolkata",
	"Asia/Krasnoyarsk",
	"Asia/Kuala_Lumpur",
	"Asia/Kuwait",
	"Asia/Macau",
	"Asia/Magadan",
	"Asia/Manila",
	"Asia/Muscat",
	"Asia/Novosibirsk",
	"Asia/Phnom_Penh",
	"Asia/Qatar",
	"Asia/Riyadh",
	"Asia/Seoul",
	"Asia/Shanghai",
	"Asia/Singapore",
	"Asia/Taipei",
	"Asia/Tashkent",
	"Asia/Tbilisi",
	"Asia/Tehran",
	"Asia/Tokyo",
	"Asia/Ulaanbaatar",
	"Asia/Vladivostok",
	"Asia/Yakutsk",
	"Asia/Yangon",
	"Asia/Yerevan",
	"Atlantic/Azores",
	"Atlantic/Canary",
	"Australia/Adelaide",
	"Australia/Brisbane",
	"Australia/Darwin",
	"Australia/Hobart",
	"Australia/Melbourne",
	"Australia/Perth",
	"Australia/Sydney",
	"Europe/Amsterdam",
	"Europe/Athens",
	"Europe/Belgrade",
	"Europe/Berlin",
	"Europe/Brussels",
	"Europe/Bucharest",
	"Europe/Budapest",
	"Europe/Copenhagen",
	"Europe/Dublin",
	"Europe/Helsinki",
	"Europe/Kyiv",
	"Europe/Lisbon",
	"Europe/Ljubljana",
	"Europe/London",
	"Europe/Madrid",
	"Europe/Minsk",
	"Europe/Moscow",
	"Europe/Oslo",
	"Europe/Paris",
	"Europe/Prague",
	"Europe/Reykjavik",
	"Europe/Riga",
	"Europe/Rome",
	"Europe/Sarajevo",
	"Europe/Skopje",
	"Europe/Sofia",
	"Europe/Stockholm",
	"Europe/Tallinn",
	"Europe/Tirane",
	"Europe/Vienna",
	"Europe/Vilnius",
	"Europe/Warsaw",
	"Europe/Zagreb",
	"Europe/Zurich",
	"Indian/Maldives",
	"Indian/Mauritius",
	"Indian/Reunion",
	"Pacific/Apia",
	"Pacific/Auckland",
	"Pacific/Fiji",
	"Pacific/Guam",
	"Pacific/Noumea",
	"Pacific/Port_Moresby",
	"Pacific/Tongatapu",
}

func buildTimezoneOptions(current string, now time.Time) []timezoneOption {
	options := make([]timezoneOption, 0, len(timezoneNames))
	seen := make(map[string]struct{}, len(timezoneNames))
	for _, name := range timezoneNames {
		label, ok := timezoneLabel(name, now)
		if !ok {
			continue
		}
		seen[name] = struct{}{}
		options = append(options, timezoneOption{
			Value:    name,
			Label:    label,
			Selected: name == current,
		})
	}
	if current != "" {
		if _, ok := seen[current]; !ok {
			if label, ok := timezoneLabel(current, now); ok {
				options = append([]timezoneOption{{
					Value: current, Label: label, Selected: true,
				}}, options...)
			}
		}
	}
	return options
}

func timezoneLabel(name string, now time.Time) (string, bool) {
	location, err := time.LoadLocation(name)
	if err != nil {
		return "", false
	}
	_, offset := now.In(location).Zone()
	if offset%1800 != 0 {
		return "", false
	}
	if name == "UTC" {
		return name, true
	}
	return fmt.Sprintf("%s (UTC%s)", name, formatUTCOffset(offset)), true
}

func formatUTCOffset(offset int) string {
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return fmt.Sprintf("%s%02d:%02d", sign, offset/3600, offset%3600/60)
}
