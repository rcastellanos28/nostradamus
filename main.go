package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	apiBase          = "https://v3.football.api-sports.io/"
	weatherBase      = "https://api.open-meteo.com/v1/forecast"
	requestTimeout   = 5 * time.Second
	worldCupLeagueID = 1
	worldCupSeason   = 2026
)

var worldCupNations = []string{
	"algeria", "argentina", "australia", "austria", "belgium", "bosnia and herzegovina", "brazil",
	"cabo verde", "canada", "colombia", "congo dr", "croatia", "curacao", "czechia",
	"ecuador", "egypt", "england", "france", "germany", "ghana", "haiti", "iran", "iraq",
	"ivory coast", "japan", "jordan", "korea republic", "mexico", "morocco", "netherlands",
	"new zealand", "norway", "panama", "paraguay", "portugal", "qatar", "saudi arabia",
	"scotland", "senegal", "south africa", "spain", "sweden", "switzerland", "tunisia",
	"turkey", "uruguay", "usa", "uzbekistan",
}

type TeamStatic struct {
	Name        string
	APISearch   string
	FIFARank    int
	Stadium     string
	Lat         float64
	Lon         float64
	AltitudeM   int
	HostNation  bool
	Captain     string
	CaptainCaps int
	GK          string
	GKCaps      int
	GKRating    float64
	PenWins     int
	PenLosses   int
}

type TeamMetrics struct {
	Static              TeamStatic
	Form                []Match
	FormScore           float64
	GFAvg               float64
	GAAvg               float64
	Depth               float64
	Cohesion            float64
	AvgAge              float64
	AgeScore            float64
	Fatigue             float64
	SetPieceThreat      float64
	CounterAttack       float64
	PressIntensity      float64
	PenaltyScore        float64
	Possession          float64
	CleanSheets         int
	Injuries            []string
	Suspensions         []string
	TacticalProfile     string
	ExcludedFactors     []string
	DataCompleteness    float64
	RecentResultsString string
}

type Match struct {
	ForGoals     int
	AgainstGoals int
	Competition  string
	Date         time.Time
}

type Weather struct {
	TempC       float64
	RainMM      float64
	WindKMH     float64
	HumidityPct float64
	Summary     string
}

type FixtureInfo struct {
	ID          int
	Date        time.Time
	Status      string
	Round       string
	HomeName    string
	AwayName    string
	HomeTeamID  int
	AwayTeamID  int
	VenueID     int
	VenueName   string
	VenueCity   string
	VenueLat    float64
	VenueLon    float64
	AltitudeM   int
	HasVenueGeo bool
}

type Prediction struct {
	HomeScore       float64
	AwayScore       float64
	HomeProb        float64
	DrawProb        float64
	AwayProb        float64
	HomeGoals       int
	AwayGoals       int
	KeyFactors      []string
	HomeModifier    float64
	AwayModifier    float64
	ExcludedFactors []string
	LimitedData     bool
}

type apiTeamSearch struct {
	Response []struct {
		Team struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
	} `json:"response"`
}

type apiFixtures struct {
	Response []struct {
		Fixture struct {
			ID     int    `json:"id"`
			Date   string `json:"date"`
			Status struct {
				Short string `json:"short"`
			} `json:"status"`
			Venue struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
				City string `json:"city"`
			} `json:"venue"`
		} `json:"fixture"`
		League struct {
			Name  string `json:"name"`
			Round string `json:"round"`
		} `json:"league"`
		Goals struct {
			Home *int `json:"home"`
			Away *int `json:"away"`
		} `json:"goals"`
		Teams struct {
			Home struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"home"`
			Away struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"away"`
		} `json:"teams"`
	} `json:"response"`
}

type apiInjuries struct {
	Response []struct {
		Player struct {
			Name string `json:"name"`
		} `json:"player"`
	} `json:"response"`
}

type apiSquad struct {
	Response []struct {
		Players []struct {
			Name     string `json:"name"`
			Age      int    `json:"age"`
			Position string `json:"position"`
		} `json:"players"`
	} `json:"response"`
}

type openMeteo struct {
	Current struct {
		Temperature float64 `json:"temperature_2m"`
		Rain        float64 `json:"rain"`
		Wind        float64 `json:"wind_speed_10m"`
		Humidity    float64 `json:"relative_humidity_2m"`
	} `json:"current"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	key := strings.TrimSpace(os.Getenv("API_FOOTBALL_KEY"))
	if key == "" {
		printMissingKey()
		return
	}

	client := &http.Client{Timeout: requestTimeout}

	switch os.Args[1] {
	case "--list-fixtures":
		round := parseFlagValue(os.Args[2:], "--round")
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		if err := listFixturesMode(ctx, client, key, round); err != nil {
			fmt.Printf("Could not list fixtures: %v\n", err)
		}
		return
	case "--fixture":
		if len(os.Args) < 3 {
			fmt.Println("Missing fixture ID.")
			fmt.Println("Usage: go run main.go --fixture 98232")
			return
		}
		fixtureID, err := strconv.Atoi(strings.TrimSpace(os.Args[2]))
		if err != nil {
			fmt.Printf("Invalid fixture ID %q. Use a numeric API-Football fixture ID.\n", os.Args[2])
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		if err := fixtureMode(ctx, client, key, fixtureID); err != nil {
			fmt.Printf("Could not predict fixture %d: %v\n", fixtureID, err)
		}
		return
	}

	if len(os.Args) < 3 {
		printUsage()
		return
	}

	home, ok := lookupTeam(os.Args[1])
	if !ok {
		fmt.Printf("Unknown home team %q. Did you mean %q?\n", os.Args[1], closestNation(os.Args[1]))
		return
	}
	away, ok := lookupTeam(os.Args[2])
	if !ok {
		fmt.Printf("Unknown away team %q. Did you mean %q?\n", os.Args[2], closestNation(os.Args[2]))
		return
	}

	stage := "group"
	if len(os.Args) >= 4 && strings.TrimSpace(os.Args[3]) != "" {
		stage = strings.ToLower(strings.TrimSpace(os.Args[3]))
	}
	homeRest, awayRest := 4, 4
	if len(os.Args) >= 5 {
		homeRest, awayRest = parseRest(os.Args[4])
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	runPrediction(ctx, client, key, home, away, stage, homeRest, awayRest, home.Lat, home.Lon)
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  go run main.go \"brazil\" \"argentina\" [stage] [days_rest]")
	fmt.Println("  go run main.go --list-fixtures [--round \"Round of 16\"]")
	fmt.Println("  go run main.go --fixture 98232")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  go run main.go \"brazil\" \"argentina\"")
	fmt.Println("  go run main.go \"france\" \"england\" quarter 3")
	fmt.Println("  go run main.go --list-fixtures --round \"Round of 16\"")
}

func printMissingKey() {
	fmt.Println("Missing API key.")
	fmt.Println()
	fmt.Println("Setup:")
	fmt.Println("  export API_FOOTBALL_KEY=\"your_api_football_key\"")
	fmt.Println("  go run main.go \"brazil\" \"argentina\"")
	fmt.Println("  go run main.go --list-fixtures --round \"Round of 16\"")
	fmt.Println("  go run main.go --fixture 98232")
	fmt.Println()
	fmt.Println("The key is read with os.Getenv(\"API_FOOTBALL_KEY\") and is never hardcoded.")
}

func parseRest(arg string) (int, int) {
	parts := strings.FieldsFunc(arg, func(r rune) bool { return r == ':' || r == ',' || r == '/' })
	if len(parts) == 2 {
		h, herr := strconv.Atoi(strings.TrimSpace(parts[0]))
		a, aerr := strconv.Atoi(strings.TrimSpace(parts[1]))
		if herr == nil && aerr == nil {
			return h, a
		}
	}
	v, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil {
		return 4, 4
	}
	return v, v
}

func parseFlagValue(args []string, flag string) string {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return strings.TrimSpace(args[i+1])
		}
		if strings.HasPrefix(arg, flag+"=") {
			return strings.TrimSpace(strings.TrimPrefix(arg, flag+"="))
		}
	}
	return ""
}

func listFixturesMode(ctx context.Context, client *http.Client, key, round string) error {
	fixtures, err := fetchWCFixtures(ctx, client, key, round)
	if err != nil {
		return err
	}
	if len(fixtures) == 0 {
		if round == "" {
			fmt.Println("No upcoming WC2026 fixtures returned by API-Football.")
		} else {
			fmt.Printf("No WC2026 fixtures returned for round %q.\n", round)
		}
		return nil
	}
	printFixtures(fixtures, round == "")
	return nil
}

func fixtureMode(ctx context.Context, client *http.Client, key string, fixtureID int) error {
	fixture, err := fetchFixtureByID(ctx, client, key, fixtureID)
	if err != nil {
		return err
	}
	home, ok := lookupTeam(fixture.HomeName)
	if !ok {
		return fmt.Errorf("unknown home team %q from fixture response; closest known team is %q", fixture.HomeName, closestNation(fixture.HomeName))
	}
	away, ok := lookupTeam(fixture.AwayName)
	if !ok {
		return fmt.Errorf("unknown away team %q from fixture response; closest known team is %q", fixture.AwayName, closestNation(fixture.AwayName))
	}

	lat, lon, altitude, hasGeo := venueCoordsForFixture(fixture)
	if hasGeo {
		fixture.VenueLat, fixture.VenueLon, fixture.AltitudeM, fixture.HasVenueGeo = lat, lon, altitude, true
	}
	updateVenue(&home, fixture)
	updateVenue(&away, fixture)

	homeRest := restDaysBefore(ctx, client, key, fixture.HomeTeamID, fixture.Date)
	awayRest := restDaysBefore(ctx, client, key, fixture.AwayTeamID, fixture.Date)
	stage := stageFromRound(fixture.Round)
	runPrediction(ctx, client, key, home, away, stage, homeRest, awayRest, lat, lon)
	return nil
}

func runPrediction(ctx context.Context, client *http.Client, key string, home, away TeamStatic, stage string, homeRest, awayRest int, weatherLat, weatherLon float64) {
	homeMetrics, homeLimited := buildTeamMetrics(ctx, client, key, home)
	awayMetrics, awayLimited := buildTeamMetrics(ctx, client, key, away)
	weather, weatherLimited := fetchWeather(ctx, client, weatherLat, weatherLon)

	prediction := predict(homeMetrics, awayMetrics, weather, stage, homeRest, awayRest)
	prediction.LimitedData = homeLimited || awayLimited || weatherLimited

	printReport(homeMetrics, awayMetrics, weather, prediction, stage, homeRest, awayRest)
}

func fetchWCFixtures(ctx context.Context, client *http.Client, key, round string) ([]FixtureInfo, error) {
	today := time.Now().Format("2006-01-02")
	through := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	endpoint := fmt.Sprintf("%sfixtures?league=%d&season=%d&from=%s&to=%s", apiBase, worldCupLeagueID, worldCupSeason, today, through)
	if round != "" {
		endpoint += "&round=" + url.QueryEscape(round)
	}
	var parsed apiFixtures
	if err := getJSON(ctx, client, key, endpoint, &parsed); err != nil {
		return nil, err
	}
	fixtures := make([]FixtureInfo, 0, len(parsed.Response))
	for _, item := range parsed.Response {
		fixtures = append(fixtures, fixtureFromAPI(item))
	}
	sort.Slice(fixtures, func(i, j int) bool {
		return fixtures[i].Date.Before(fixtures[j].Date)
	})
	return fixtures, nil
}

func fetchFixtureByID(ctx context.Context, client *http.Client, key string, id int) (FixtureInfo, error) {
	var parsed apiFixtures
	if err := getJSON(ctx, client, key, fmt.Sprintf("%sfixtures?id=%d", apiBase, id), &parsed); err != nil {
		return FixtureInfo{}, err
	}
	if len(parsed.Response) == 0 {
		return FixtureInfo{}, fmt.Errorf("fixture ID %d was not found", id)
	}
	return fixtureFromAPI(parsed.Response[0]), nil
}

func fixtureFromAPI(item struct {
	Fixture struct {
		ID     int    `json:"id"`
		Date   string `json:"date"`
		Status struct {
			Short string `json:"short"`
		} `json:"status"`
		Venue struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			City string `json:"city"`
		} `json:"venue"`
	} `json:"fixture"`
	League struct {
		Name  string `json:"name"`
		Round string `json:"round"`
	} `json:"league"`
	Goals struct {
		Home *int `json:"home"`
		Away *int `json:"away"`
	} `json:"goals"`
	Teams struct {
		Home struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"home"`
		Away struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"away"`
	} `json:"teams"`
}) FixtureInfo {
	when, _ := time.Parse(time.RFC3339, item.Fixture.Date)
	fixture := FixtureInfo{
		ID:         item.Fixture.ID,
		Date:       when,
		Status:     item.Fixture.Status.Short,
		Round:      item.League.Round,
		HomeName:   item.Teams.Home.Name,
		AwayName:   item.Teams.Away.Name,
		HomeTeamID: item.Teams.Home.ID,
		AwayTeamID: item.Teams.Away.ID,
		VenueID:    item.Fixture.Venue.ID,
		VenueName:  item.Fixture.Venue.Name,
		VenueCity:  item.Fixture.Venue.City,
	}
	fixture.VenueLat, fixture.VenueLon, fixture.AltitudeM, fixture.HasVenueGeo = venueCoordsForFixture(fixture)
	return fixture
}

func printFixtures(fixtures []FixtureInfo, grouped bool) {
	currentDate := ""
	for i, f := range fixtures {
		dateGroup := "TBD"
		if !f.Date.IsZero() {
			dateGroup = f.Date.Format("2006-01-02")
		}
		if dateGroup != currentDate {
			currentDate = dateGroup
			fmt.Println()
			fmt.Println(currentDate)
			fmt.Println(strings.Repeat("-", len(currentDate)))
		}

		timeOfDay := "TBD"
		if !f.Date.IsZero() {
			timeOfDay = f.Date.Format("15:04 MST")
		}
		venue := strings.TrimSpace(f.VenueName)
		if venue == "" {
			venue = "TBD venue"
		}
		city := strings.TrimSpace(f.VenueCity)
		if city == "" {
			city = "TBD city"
		}
		fmt.Printf("%2d. #%d | %s | %s | %s vs %s | %s, %s\n", i+1, f.ID, fixtureStatus(f.Status), timeOfDay, f.HomeName, f.AwayName, venue, city)
	}
}

func fixtureStatus(short string) string {
	s := strings.ToUpper(strings.TrimSpace(short))
	switch s {
	case "", "TBD":
		return "TBD"
	case "NS", "FT", "AET", "PEN", "PST", "CANC", "ABD", "AWD", "WO":
		return s
	default:
		return "LIVE"
	}
}

func stageFromRound(round string) string {
	s := strings.ToLower(round)
	switch {
	case strings.Contains(s, "round of 16") || strings.Contains(s, "last 16"):
		return "round of 16"
	case strings.Contains(s, "quarter"):
		return "quarter"
	case strings.Contains(s, "semi"):
		return "semi"
	case strings.Contains(s, "final"):
		return "final"
	default:
		return "group"
	}
}

func restDaysBefore(ctx context.Context, client *http.Client, key string, teamID int, fixtureDate time.Time) int {
	if teamID == 0 || fixtureDate.IsZero() {
		return 4
	}
	to := fixtureDate.AddDate(0, 0, -1).Format("2006-01-02")
	url := fmt.Sprintf("%sfixtures?team=%d&league=%d&season=%d&to=%s&last=1", apiBase, teamID, worldCupLeagueID, worldCupSeason, to)
	var parsed apiFixtures
	if err := getJSON(ctx, client, key, url, &parsed); err != nil || len(parsed.Response) == 0 {
		return 4
	}
	previous, err := time.Parse(time.RFC3339, parsed.Response[0].Fixture.Date)
	if err != nil || previous.IsZero() {
		return 4
	}
	days := int(fixtureDate.Sub(previous).Hours() / 24)
	if days <= 0 {
		return 4
	}
	return clampInt(days, 1, 10)
}

func updateVenue(team *TeamStatic, fixture FixtureInfo) {
	venue := strings.TrimSpace(fixture.VenueName)
	city := strings.TrimSpace(fixture.VenueCity)
	if venue != "" && city != "" {
		team.Stadium = venue + ", " + city
	} else if venue != "" {
		team.Stadium = venue
	}
	if fixture.HasVenueGeo {
		team.Lat = fixture.VenueLat
		team.Lon = fixture.VenueLon
		team.AltitudeM = fixture.AltitudeM
	}
}

func venueCoordsForFixture(f FixtureInfo) (float64, float64, int, bool) {
	if f.HasVenueGeo {
		return f.VenueLat, f.VenueLon, f.AltitudeM, true
	}
	if lat, lon, altitude, ok := venueCoordinate(f.VenueName); ok {
		return lat, lon, altitude, true
	}
	if lat, lon, altitude, ok := venueCoordinate(f.VenueCity); ok {
		return lat, lon, altitude, true
	}
	return 39.8283, -98.5795, 0, false
}

func venueCoordinate(name string) (float64, float64, int, bool) {
	venues := map[string]struct {
		Lat      float64
		Lon      float64
		Altitude int
	}{
		"atlanta": {33.7554, -84.4008, 320}, "mercedes-benz stadium": {33.7554, -84.4008, 320},
		"boston": {42.0909, -71.2643, 80}, "gillette stadium": {42.0909, -71.2643, 80},
		"dallas": {32.7473, -97.0945, 163}, "at&t stadium": {32.7473, -97.0945, 163},
		"guadalajara": {20.6817, -103.4627, 1566}, "estadio akron": {20.6817, -103.4627, 1566},
		"houston": {29.6847, -95.4107, 13}, "nrg stadium": {29.6847, -95.4107, 13},
		"kansas city": {39.0490, -94.4839, 265}, "arrowhead stadium": {39.0490, -94.4839, 265},
		"los angeles": {33.9535, -118.3392, 38}, "sofi stadium": {33.9535, -118.3392, 38},
		"mexico city": {19.3029, -99.1505, 2200}, "estadio azteca": {19.3029, -99.1505, 2200},
		"miami": {25.9580, -80.2389, 2}, "hard rock stadium": {25.9580, -80.2389, 2},
		"monterrey": {25.6682, -100.2445, 540}, "estadio bbva": {25.6682, -100.2445, 540},
		"new york new jersey": {40.8135, -74.0745, 2}, "new york/new jersey": {40.8135, -74.0745, 2}, "metlife stadium": {40.8135, -74.0745, 2},
		"philadelphia": {39.9008, -75.1675, 3}, "lincoln financial field": {39.9008, -75.1675, 3},
		"san francisco bay area": {37.4030, -121.9700, 12}, "levi's stadium": {37.4030, -121.9700, 12},
		"seattle": {47.5952, -122.3316, 56}, "lumen field": {47.5952, -122.3316, 56},
		"toronto": {43.6332, -79.4186, 76}, "bmo field": {43.6332, -79.4186, 76},
		"vancouver": {49.2767, -123.1119, 2}, "bc place": {49.2767, -123.1119, 2},
	}
	v, ok := venues[normalizeName(name)]
	return v.Lat, v.Lon, v.Altitude, ok
}

func buildTeamMetrics(ctx context.Context, client *http.Client, key string, static TeamStatic) (TeamMetrics, bool) {
	m := fallbackMetrics(static)
	teamID, err := fetchTeamID(ctx, client, key, static.APISearch)
	if err != nil {
		m.ExcludedFactors = append(m.ExcludedFactors, "live team lookup")
		return m, true
	}

	limited := false
	if fixtures, err := fetchFixtures(ctx, client, key, teamID); err == nil && len(fixtures) > 0 {
		m.Form = fixtures
		m.FormScore, m.GFAvg, m.GAAvg, m.SetPieceThreat = computeForm(fixtures)
	} else {
		m.ExcludedFactors = append(m.ExcludedFactors, "live last 10 fixtures")
		limited = true
	}
	if injuries, err := fetchInjuries(ctx, client, key, teamID); err == nil {
		m.Injuries = injuries
	} else {
		m.ExcludedFactors = append(m.ExcludedFactors, "current injuries/suspensions")
	}
	if squad, err := fetchSquad(ctx, client, key, teamID); err == nil && len(squad) > 0 {
		m.AvgAge = averageAge(squad)
		m.Depth = squadDepth(squad)
		m.Cohesion = clamp(4+(float64(len(squad))/26)*2+sameClubFallback(static.Name), 1, 10)
		m.AgeScore = ageScore(m.AvgAge)
	} else {
		m.ExcludedFactors = append(m.ExcludedFactors, "live squad positions/caps")
	}

	m.TacticalProfile = tacticalProfile(m)
	m.RecentResultsString = formString(m.Form)
	m.DataCompleteness = dataCompleteness(m)
	return m, limited
}

func fetchTeamID(ctx context.Context, client *http.Client, key, name string) (int, error) {
	var parsed apiTeamSearch
	if err := getJSON(ctx, client, key, apiBase+"teams?search="+urlQuery(name), &parsed); err != nil {
		return 0, err
	}
	if len(parsed.Response) == 0 {
		return 0, errors.New("team not found")
	}
	return parsed.Response[0].Team.ID, nil
}

func fetchFixtures(ctx context.Context, client *http.Client, key string, teamID int) ([]Match, error) {
	var parsed apiFixtures
	url := fmt.Sprintf("%sfixtures?team=%d&last=10", apiBase, teamID)
	if err := getJSON(ctx, client, key, url, &parsed); err != nil {
		return nil, err
	}
	matches := make([]Match, 0, len(parsed.Response))
	for _, f := range parsed.Response {
		if f.Goals.Home == nil || f.Goals.Away == nil {
			continue
		}
		forGoals, againstGoals := *f.Goals.Home, *f.Goals.Away
		if f.Teams.Away.ID == teamID {
			forGoals, againstGoals = *f.Goals.Away, *f.Goals.Home
		}
		when, _ := time.Parse(time.RFC3339, f.Fixture.Date)
		matches = append(matches, Match{ForGoals: forGoals, AgainstGoals: againstGoals, Competition: f.League.Name, Date: when})
	}
	return matches, nil
}

func fetchInjuries(ctx context.Context, client *http.Client, key string, teamID int) ([]string, error) {
	var parsed apiInjuries
	url := fmt.Sprintf("%sinjuries?team=%d&season=%d", apiBase, teamID, worldCupSeason)
	if err := getJSON(ctx, client, key, url, &parsed); err != nil {
		return nil, err
	}
	var names []string
	seen := map[string]bool{}
	for _, item := range parsed.Response {
		name := strings.TrimSpace(item.Player.Name)
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names, nil
}

func fetchSquad(ctx context.Context, client *http.Client, key string, teamID int) ([]struct {
	Name     string
	Age      int
	Position string
}, error) {
	var parsed apiSquad
	if err := getJSON(ctx, client, key, fmt.Sprintf("%splayers/squads?team=%d", apiBase, teamID), &parsed); err != nil {
		return nil, err
	}
	var players []struct {
		Name     string
		Age      int
		Position string
	}
	for _, group := range parsed.Response {
		for _, p := range group.Players {
			players = append(players, struct {
				Name     string
				Age      int
				Position string
			}{p.Name, p.Age, p.Position})
		}
	}
	return players, nil
}

func getJSON(ctx context.Context, client *http.Client, key, url string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-apisports-key", key)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("api status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func fetchWeather(ctx context.Context, client *http.Client, lat, lon float64) (Weather, bool) {
	url := fmt.Sprintf("%s?latitude=%.4f&longitude=%.4f&current=temperature_2m,rain,wind_speed_10m,relative_humidity_2m&wind_speed_unit=kmh", weatherBase, lat, lon)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fallbackWeather(), true
	}
	resp, err := client.Do(req)
	if err != nil {
		return fallbackWeather(), true
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fallbackWeather(), true
	}
	var parsed openMeteo
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fallbackWeather(), true
	}
	w := Weather{
		TempC:       parsed.Current.Temperature,
		RainMM:      parsed.Current.Rain,
		WindKMH:     parsed.Current.Wind,
		HumidityPct: parsed.Current.Humidity,
	}
	w.Summary = weatherSummary(w)
	return w, false
}

func fallbackWeather() Weather {
	return Weather{TempC: 22, RainMM: 0, WindKMH: 10, HumidityPct: 55, Summary: "mild fallback weather"}
}

func predict(home, away TeamMetrics, weather Weather, stage string, homeRest, awayRest int) Prediction {
	homeBase := weightedScore(home)
	awayBase := weightedScore(away)

	homeMod, awayMod := contextModifiers(home, away, weather, homeRest, awayRest)
	stageMult := stageMultiplier(stage)
	if isKnockout(stage) {
		homeBase *= stageMult
		awayBase *= stageMult
	}
	homeScore := homeBase + homeMod
	awayScore := awayBase + awayMod
	matchupAdjust(&homeScore, &awayScore, home, away)

	diff := clamp(homeScore-awayScore, -45, 45)
	draw := 24.0 - math.Abs(diff)*0.22
	if isKnockout(stage) {
		draw += 7
	}
	draw = clamp(draw, 5, 40)
	remaining := 100 - draw
	homeWin := remaining * (1 / (1 + math.Exp(-diff/16)))
	awayWin := remaining - homeWin
	homeWin = clamp(homeWin, 5, 85)
	awayWin = clamp(awayWin, 5, 85)
	draw = clamp(100-homeWin-awayWin, 5, 40)
	total := homeWin + draw + awayWin
	homeWin, draw, awayWin = homeWin/total*100, draw/total*100, awayWin/total*100

	hg, ag := scoreline(home, away, stage, homeScore, awayScore)
	return Prediction{
		HomeScore:       homeScore,
		AwayScore:       awayScore,
		HomeProb:        homeWin,
		DrawProb:        draw,
		AwayProb:        awayWin,
		HomeGoals:       hg,
		AwayGoals:       ag,
		KeyFactors:      keyFactors(home, away, weather, stage, homeMod, awayMod),
		HomeModifier:    homeMod,
		AwayModifier:    awayMod,
		ExcludedFactors: append(home.ExcludedFactors, away.ExcludedFactors...),
	}
}

func weightedScore(m TeamMetrics) float64 {
	ranking := clamp(100-(float64(m.Static.FIFARank)-1)*1.7, 25, 100)
	defense := clamp(100-(m.GAAvg*35*1.4), 10, 100)
	attack := clamp(m.GFAvg*35, 10, 100)
	gk := clamp(m.Static.GKRating*10+(float64(m.Static.GKCaps)/100)*10, 35, 100)
	injuries := clamp(100-float64(len(m.Injuries))*9-float64(len(m.Suspensions))*7, 35, 100)
	fatigue := clamp(100-m.Fatigue*9, 20, 100)
	age := m.AgeScore * 10
	setPiece := clamp(m.SetPieceThreat*100, 0, 100)
	captain := 55.0
	if m.Static.CaptainCaps > 80 {
		captain = 85
	} else if m.Static.CaptainCaps > 50 {
		captain = 72
	}

	return ranking*.20 + m.FormScore*.20 + defense*.15 + attack*.10 + m.Cohesion*10*.10 +
		gk*.08 + injuries*.08 + fatigue*.05 + age*.04 + setPiece*.03 + captain*.03
}

func contextModifiers(home, away TeamMetrics, weather Weather, homeRest, awayRest int) (float64, float64) {
	homeMod, awayMod := 0.0, 0.0
	if home.Static.HostNation {
		homeMod += 10
	}
	if away.Static.HostNation {
		awayMod += 10
	}
	if homeRest < 3 {
		homeMod -= float64(3-homeRest) * 2.5
	}
	if awayRest < 3 {
		awayMod -= float64(3-awayRest) * 2.5
	}
	if home.Static.AltitudeM > 2000 {
		awayMod -= 5
	}
	if weather.RainMM > 5 {
		homeMod += 2
	}
	if weather.WindKMH > 35 {
		homeMod += 3
	}
	if weather.TempC > 30 {
		awayMod -= 2
	}
	if weather.TempC < 2 {
		homeMod += 1
	}
	if weather.HumidityPct > 80 {
		awayMod -= 1
	}
	return homeMod, awayMod
}

func matchupAdjust(homeScore, awayScore *float64, home, away TeamMetrics) {
	if strings.Contains(home.TacticalProfile, "possession") && away.PressIntensity > 7 {
		*awayScore += 1.5
	}
	if strings.Contains(away.TacticalProfile, "possession") && home.PressIntensity > 7 {
		*homeScore += 1.5
	}
	if strings.Contains(home.TacticalProfile, "defensive") && strings.Contains(away.TacticalProfile, "attacking") {
		*homeScore += 1
		*awayScore -= 1
	}
	if strings.Contains(away.TacticalProfile, "defensive") && strings.Contains(home.TacticalProfile, "attacking") {
		*awayScore += 1
		*homeScore -= 1
	}
}

func scoreline(home, away TeamMetrics, stage string, homeScore, awayScore float64) (int, int) {
	totalGoals := 2.6
	if isKnockout(stage) {
		totalGoals -= 0.35
	}
	if stage == "final" {
		totalGoals -= 0.25
	}
	homeExpected := (home.GFAvg + away.GAAvg*1.4) / 2
	awayExpected := (away.GFAvg + home.GAAvg*1.4) / 2
	sum := homeExpected + awayExpected
	if sum <= 0 {
		sum = 2.6
	}
	homeExpected = homeExpected / sum * totalGoals
	awayExpected = awayExpected / sum * totalGoals
	if homeScore-awayScore > 12 {
		homeExpected += .35
		awayExpected -= .15
	}
	if awayScore-homeScore > 12 {
		awayExpected += .35
		homeExpected -= .15
	}
	return clampInt(int(math.Round(clamp(homeExpected, 0, 4))), 0, 5),
		clampInt(int(math.Round(clamp(awayExpected, 0, 4))), 0, 5)
}

func computeForm(matches []Match) (formScore, gfAvg, gaAvg, setPiece float64) {
	if len(matches) == 0 {
		return 55, 1.4, 1.1, .22
	}
	var points, maxPoints, gf, ga float64
	for i, m := range matches {
		weight := 1.0
		if i < 3 {
			weight *= 1.5
		}
		name := strings.ToLower(m.Competition)
		switch {
		case strings.Contains(name, "world cup") && strings.Contains(name, "qual"):
			weight *= 1.5
		case strings.Contains(name, "friendly"):
			weight *= .6
		}
		result := 1.0
		if m.ForGoals > m.AgainstGoals {
			result = 3
		} else if m.ForGoals < m.AgainstGoals {
			result = 0
		}
		points += result * weight
		maxPoints += 3 * weight
		gf += float64(m.ForGoals) * weight
		ga += float64(m.AgainstGoals) * weight
	}
	formScore = clamp(points/maxPoints*100, 5, 100)
	gfAvg = gf / maxPoints * 3
	gaAvg = ga / maxPoints * 3
	setPiece = clamp((gfAvg*.12 + .12), .08, .38)
	return
}

func tacticalProfile(m TeamMetrics) string {
	parts := []string{}
	if m.Possession > 55 {
		parts = append(parts, "possession-based")
	}
	if m.GAAvg < .8 {
		parts = append(parts, "defensive")
	}
	if m.GFAvg > 2.2 {
		parts = append(parts, "attacking")
	}
	if m.PressIntensity > 7 {
		parts = append(parts, "high press")
	}
	if len(parts) == 0 {
		parts = append(parts, "balanced")
	}
	return strings.Join(parts, ", ")
}

func formString(matches []Match) string {
	var out strings.Builder
	for i, m := range matches {
		if i >= 10 {
			break
		}
		switch {
		case m.ForGoals > m.AgainstGoals:
			out.WriteByte('W')
		case m.ForGoals == m.AgainstGoals:
			out.WriteByte('D')
		default:
			out.WriteByte('L')
		}
	}
	if out.Len() == 0 {
		return "WDWWDWLWDW"
	}
	return out.String()
}

func stageMultiplier(stage string) float64 {
	switch normalizeStage(stage) {
	case "round of 16":
		return 1.10
	case "quarter":
		return 1.15
	case "semi":
		return 1.20
	case "final":
		return 1.25
	default:
		return 1.0
	}
}

func isKnockout(stage string) bool {
	return normalizeStage(stage) != "group"
}

func normalizeStage(stage string) string {
	s := strings.ToLower(strings.TrimSpace(stage))
	switch s {
	case "r16", "round16", "round-of-16", "round_of_16", "last16":
		return "round of 16"
	case "quarters", "quarterfinal", "quarter-final", "qf":
		return "quarter"
	case "semis", "semifinal", "semi-final", "sf":
		return "semi"
	case "final":
		return "final"
	default:
		return "group"
	}
}

func keyFactors(home, away TeamMetrics, weather Weather, stage string, homeMod, awayMod float64) []string {
	var factors []string
	add := func(s string) {
		if len(factors) < 8 {
			factors = append(factors, s)
		}
	}
	if math.Abs(home.FormScore-away.FormScore) > 8 {
		add(fmt.Sprintf("Recent form edge: %s %.0f vs %s %.0f", home.Static.Name, home.FormScore, away.Static.Name, away.FormScore))
	}
	if home.GAAvg < away.GAAvg {
		add(fmt.Sprintf("%s has the sturdier defensive profile (%.2f GA/game)", home.Static.Name, home.GAAvg))
	} else {
		add(fmt.Sprintf("%s has the sturdier defensive profile (%.2f GA/game)", away.Static.Name, away.GAAvg))
	}
	if home.Cohesion != away.Cohesion {
		add(fmt.Sprintf("Cohesion gap: %s %.1f, %s %.1f", home.Static.Name, home.Cohesion, away.Static.Name, away.Cohesion))
	}
	if len(home.Injuries)+len(home.Suspensions)+len(away.Injuries)+len(away.Suspensions) > 0 {
		add(fmt.Sprintf("Availability: %s missing %d, %s missing %d", home.Static.Name, len(home.Injuries)+len(home.Suspensions), away.Static.Name, len(away.Injuries)+len(away.Suspensions)))
	}
	if weather.RainMM > 5 || weather.WindKMH > 35 || weather.TempC > 30 || weather.HumidityPct > 80 {
		add("Weather meaningfully tilts stamina and ball control")
	}
	if home.Static.AltitudeM > 2000 {
		add(fmt.Sprintf("Venue altitude is %dm; away stamina is penalized", home.Static.AltitudeM))
	}
	if isKnockout(stage) {
		add("Knockout stage increases defensive weight and draw pressure")
	}
	if homeMod != 0 || awayMod != 0 {
		add(fmt.Sprintf("Context modifiers: %s %+0.1f, %s %+0.1f", home.Static.Name, homeMod, away.Static.Name, awayMod))
	}
	return factors
}

func printReport(home, away TeamMetrics, weather Weather, p Prediction, stage string, homeRest, awayRest int) {
	fmt.Println("⚽ WORLD CUP MATCH PREDICTOR")
	fmt.Println(strings.Repeat("=", 31))
	if p.LimitedData {
		fmt.Println("Mode: Limited data mode (live API timeout/error or missing endpoint fallback used)")
	}
	fmt.Printf("Match context: %s | Stage: %s | Weather: %s | Altitude: %dm\n",
		home.Static.Stadium, normalizeStage(stage), weather.Summary, home.Static.AltitudeM)
	fmt.Printf("Rest days: %s %d, %s %d\n\n", home.Static.Name, homeRest, away.Static.Name, awayRest)

	left := teamCardLines(home)
	right := teamCardLines(away)
	for i := 0; i < len(left) && i < len(right); i++ {
		fmt.Printf("%-45s  %s\n", left[i], right[i])
	}
	fmt.Println()

	fmt.Println("Key factors:")
	for _, f := range p.KeyFactors {
		fmt.Printf("  - %s\n", f)
	}
	if len(p.ExcludedFactors) > 0 {
		fmt.Printf("  - Excluded from scoring where unavailable: %s\n", uniqueJoined(p.ExcludedFactors))
	}
	fmt.Println()

	fmt.Println("Win probability:")
	fmt.Printf("  %-12s %s %5.1f%%\n", home.Static.Name, bar(p.HomeProb), p.HomeProb)
	fmt.Printf("  %-12s %s %5.1f%%\n", "Draw", bar(p.DrawProb), p.DrawProb)
	fmt.Printf("  %-12s %s %5.1f%%\n", away.Static.Name, bar(p.AwayProb), p.AwayProb)
	fmt.Println()

	fmt.Printf("Predicted scoreline: %s %d - %d %s\n", home.Static.Name, p.HomeGoals, p.AwayGoals, away.Static.Name)
	if isKnockout(stage) {
		hShoot, aShoot := shootoutOdds(home, away)
		fmt.Printf("Penalty shootout odds: %s %.1f%% / %s %.1f%%\n", home.Static.Name, hShoot, away.Static.Name, aShoot)
	}
	fmt.Printf("Confidence level: %s\n", confidence(home, away, p.LimitedData))
	fmt.Println("Joke line: I have never been more certain about a prediction that could be ruined by one corner kick in minute 3.")
	fmt.Println("Disclaimer: ⚠️ This model knows nothing. Enjoy responsibly.")
}

func teamCardLines(m TeamMetrics) []string {
	return []string{
		fmt.Sprintf("%s", strings.ToUpper(m.Static.Name)),
		fmt.Sprintf("  FIFA rank: #%d", m.Static.FIFARank),
		fmt.Sprintf("  Form: %-10s %s", m.RecentResultsString, miniFormBar(m.RecentResultsString)),
		fmt.Sprintf("  Tactical: %s", m.TacticalProfile),
		fmt.Sprintf("  GK: %s", m.Static.GK),
		fmt.Sprintf("  Captain caps: %s %d", m.Static.Captain, m.Static.CaptainCaps),
		fmt.Sprintf("  Cohesion: %.1f/10", m.Cohesion),
		fmt.Sprintf("  Fatigue: %.1f/10", m.Fatigue),
		fmt.Sprintf("  Injury count: %d", len(m.Injuries)+len(m.Suspensions)),
	}
}

func bar(pct float64) string {
	filled := int(math.Round(pct / 5))
	if filled < 1 {
		filled = 1
	}
	if filled > 20 {
		filled = 20
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat(".", 20-filled) + "]"
}

func miniFormBar(form string) string {
	var b strings.Builder
	for _, r := range form {
		switch r {
		case 'W':
			b.WriteByte('#')
		case 'D':
			b.WriteByte('=')
		default:
			b.WriteByte('.')
		}
	}
	return b.String()
}

func shootoutOdds(home, away TeamMetrics) (float64, float64) {
	h := 50 + (home.PenaltyScore-away.PenaltyScore)*18 + (float64(home.Static.GKCaps-away.Static.GKCaps) / 100 * 4)
	h = clamp(h, 35, 65)
	return h, 100 - h
}

func confidence(home, away TeamMetrics, limited bool) string {
	avg := (home.DataCompleteness + away.DataCompleteness) / 2
	if limited || avg < .55 {
		return "Low"
	}
	if avg < .78 {
		return "Medium"
	}
	return "High"
}

func fallbackMetrics(static TeamStatic) TeamMetrics {
	gf, ga, possession, cohesion, fatigue, depth, avgAge, press := fallbackNumbers(static.Name)
	form := []Match{
		{2, 0, "World Cup Qualifiers", time.Now()}, {1, 1, "Friendly", time.Now()}, {3, 1, "Continental Cup", time.Now()},
		{1, 0, "World Cup Qualifiers", time.Now()}, {0, 0, "Friendly", time.Now()}, {2, 1, "World Cup Qualifiers", time.Now()},
		{1, 2, "Continental Cup", time.Now()}, {2, 0, "Friendly", time.Now()}, {1, 0, "World Cup Qualifiers", time.Now()}, {2, 2, "Friendly", time.Now()},
	}
	formScore, _, _, setPiece := computeForm(form)
	penTotal := static.PenWins + static.PenLosses
	penalty := .5
	if penTotal > 0 {
		penalty = float64(static.PenWins) / float64(penTotal)
	}
	m := TeamMetrics{
		Static:           static,
		Form:             form,
		FormScore:        formScore,
		GFAvg:            gf,
		GAAvg:            ga,
		Depth:            depth,
		Cohesion:         cohesion,
		AvgAge:           avgAge,
		AgeScore:         ageScore(avgAge),
		Fatigue:          fatigue,
		SetPieceThreat:   setPiece,
		CounterAttack:    .18,
		PressIntensity:   press,
		PenaltyScore:     penalty,
		Possession:       possession,
		CleanSheets:      4,
		DataCompleteness: .52,
	}
	m.TacticalProfile = tacticalProfile(m)
	m.RecentResultsString = formString(m.Form)
	return m
}

func fallbackNumbers(name string) (gf, ga, possession, cohesion, fatigue, depth, avgAge, press float64) {
	switch strings.ToLower(name) {
	case "brazil":
		return 2.1, .8, 58, 8.4, 4.0, 8.8, 27.4, 7.3
	case "argentina":
		return 1.9, .7, 57, 8.7, 4.4, 8.2, 28.6, 6.9
	case "france":
		return 2.0, .9, 55, 8.1, 5.0, 9.0, 27.1, 7.2
	case "england":
		return 1.8, .8, 56, 7.7, 4.8, 8.4, 26.8, 6.8
	case "spain":
		return 2.0, .9, 63, 8.0, 4.1, 8.1, 26.5, 7.5
	case "germany":
		return 2.1, 1.1, 60, 7.4, 4.7, 8.0, 27.3, 7.0
	default:
		rankPenalty := float64(staticRank(name)) / 20
		return clamp(1.7-rankPenalty*.06, .8, 2.2), clamp(.8+rankPenalty*.05, .6, 1.8), 51, 6.7, 5.0, 6.6, 27.2, 5.8
	}
}

func lookupTeam(input string) (TeamStatic, bool) {
	key := normalizeName(input)
	for _, t := range staticTeams() {
		if normalizeName(t.Name) == key || normalizeName(t.APISearch) == key {
			return t, true
		}
	}
	return TeamStatic{}, false
}

func staticTeams() []TeamStatic {
	return []TeamStatic{
		{"Algeria", "Algeria", 36, "Stade Nelson Mandela", 36.7044, 3.1408, 70, false, "Riyad Mahrez", 95, "Anthony Mandrea", 25, 7.0, 0, 1},
		{"Argentina", "Argentina", 1, "Estadio Monumental", -34.5453, -58.4498, 25, false, "Lionel Messi", 180, "Emiliano Martinez", 45, 8.3, 6, 5},
		{"Australia", "Australia", 24, "Stadium Australia", -33.8472, 151.0633, 19, false, "Mathew Ryan", 90, "Mathew Ryan", 90, 7.2, 1, 0},
		{"Austria", "Austria", 22, "Ernst Happel Stadion", 48.2072, 16.4205, 171, false, "David Alaba", 105, "Patrick Pentz", 20, 7.1, 0, 0},
		{"Belgium", "Belgium", 8, "King Baudouin Stadium", 50.8956, 4.3347, 50, false, "Kevin De Bruyne", 105, "Thibaut Courtois", 102, 8.5, 0, 1},
		{"Bosnia and Herzegovina", "Bosnia-Herzegovina", 45, "Bilino Polje", 44.2039, 17.9078, 316, false, "Edin Dzeko", 135, "Ibrahim Sehic", 55, 6.9, 0, 0},
		{"Brazil", "Brazil", 5, "Maracana", -22.9122, -43.2302, 11, false, "Casemiro", 75, "Alisson", 65, 8.4, 4, 5},
		{"Cabo Verde", "Cape Verde Islands", 71, "Estadio Nacional de Cabo Verde", 14.9245, -23.5422, 97, false, "Ryan Mendes", 75, "Vozinha", 70, 6.8, 0, 0},
		{"Canada", "Canada", 49, "BMO Field", 43.6332, -79.4186, 76, true, "Alphonso Davies", 50, "Milan Borjan", 80, 7.0, 0, 0},
		{"Colombia", "Colombia", 15, "Estadio Metropolitano", 10.9269, -74.8019, 18, false, "James Rodriguez", 110, "Camilo Vargas", 30, 7.4, 1, 1},
		{"Congo DR", "Congo DR", 61, "Stade des Martyrs", -4.3303, 15.3107, 277, false, "Chancel Mbemba", 80, "Lionel Mpasi", 20, 6.9, 0, 0},
		{"Croatia", "Croatia", 10, "Stadion Maksimir", 45.8188, 16.0182, 122, false, "Luka Modric", 170, "Dominik Livakovic", 55, 7.8, 3, 1},
		{"Curacao", "Curacao", 82, "Ergilio Hato Stadium", 12.1696, -68.9590, 9, false, "Leandro Bacuna", 50, "Eloy Room", 45, 6.8, 0, 0},
		{"Czechia", "Czech Republic", 39, "Fortuna Arena", 50.0675, 14.4712, 245, false, "Tomas Soucek", 75, "Jindrich Stanek", 15, 7.0, 0, 1},
		{"Ecuador", "Ecuador", 31, "Estadio Rodrigo Paz Delgado", -0.1075, -78.4891, 2734, false, "Enner Valencia", 85, "Alexander Dominguez", 70, 7.0, 0, 0},
		{"Egypt", "Egypt", 34, "Cairo International Stadium", 30.0691, 31.3123, 74, false, "Mohamed Salah", 100, "Mohamed El Shenawy", 65, 7.2, 1, 1},
		{"England", "England", 4, "Wembley Stadium", 51.5560, -0.2796, 45, false, "Harry Kane", 95, "Jordan Pickford", 65, 7.8, 3, 7},
		{"France", "France", 2, "Stade de France", 48.9244, 2.3601, 35, false, "Kylian Mbappe", 85, "Mike Maignan", 25, 8.0, 4, 4},
		{"Germany", "Germany", 16, "Olympiastadion Berlin", 52.5147, 13.2394, 34, false, "Joshua Kimmich", 90, "Manuel Neuer", 120, 8.2, 4, 3},
		{"Ghana", "Ghana", 60, "Baba Yara Stadium", 6.6828, -1.6057, 250, false, "Andre Ayew", 115, "Lawrence Ati-Zigi", 25, 6.8, 1, 1},
		{"Haiti", "Haiti", 88, "Stade Sylvio Cator", 18.5392, -72.3350, 16, false, "Duckens Nazon", 65, "Johny Placide", 70, 6.7, 0, 0},
		{"IR Iran", "Iran", 20, "Azadi Stadium", 35.7248, 51.2753, 1273, false, "Ehsan Hajsafi", 140, "Alireza Beiranvand", 70, 7.2, 0, 0},
		{"Iraq", "Iraq", 55, "Basra International Stadium", 30.5155, 47.7804, 3, false, "Ali Adnan", 95, "Jalal Hassan", 75, 6.9, 0, 0},
		{"Cote d'Ivoire", "Ivory Coast", 33, "Stade Olympique Alassane Ouattara", 5.3982, -4.0116, 62, false, "Serge Aurier", 90, "Yahia Fofana", 25, 7.1, 1, 1},
		{"Japan", "Japan", 18, "Japan National Stadium", 35.6779, 139.7143, 40, false, "Wataru Endo", 70, "Zion Suzuki", 20, 7.1, 1, 1},
		{"Jordan", "Jordan", 68, "Amman International Stadium", 31.9869, 35.9033, 777, false, "Baha Faisal", 75, "Yazeed Abulaila", 55, 6.8, 0, 0},
		{"Korea Republic", "South Korea", 23, "Seoul World Cup Stadium", 37.5683, 126.8972, 23, false, "Son Heung-min", 125, "Kim Seung-gyu", 80, 7.2, 1, 0},
		{"Mexico", "Mexico", 14, "Estadio Azteca", 19.3029, -99.1505, 2200, true, "Guillermo Ochoa", 150, "Guillermo Ochoa", 150, 7.7, 2, 3},
		{"Morocco", "Morocco", 12, "Stade Mohammed V", 33.5828, -7.6466, 8, false, "Romain Saiss", 80, "Yassine Bounou", 65, 8.0, 2, 0},
		{"Netherlands", "Netherlands", 7, "Johan Cruyff Arena", 52.3142, 4.9418, -4, false, "Virgil van Dijk", 75, "Bart Verbruggen", 20, 7.7, 2, 1},
		{"New Zealand", "New Zealand", 103, "Eden Park", -36.8751, 174.7447, 48, false, "Chris Wood", 75, "Max Crocombe", 10, 6.7, 0, 0},
		{"Norway", "Norway", 44, "Ullevaal Stadion", 59.9490, 10.7344, 98, false, "Martin Odegaard", 65, "Orjan Nyland", 55, 7.1, 0, 0},
		{"Panama", "Panama", 35, "Estadio Rommel Fernandez", 9.0351, -79.4695, 20, false, "Anibal Godoy", 125, "Luis Mejia", 55, 6.9, 0, 0},
		{"Paraguay", "Paraguay", 48, "Estadio Defensores del Chaco", -25.2921, -57.6575, 43, false, "Gustavo Gomez", 75, "Gatito Fernandez", 50, 7.0, 0, 0},
		{"Portugal", "Portugal", 6, "Estadio da Luz", 38.7528, -9.1847, 85, false, "Cristiano Ronaldo", 200, "Diogo Costa", 30, 7.7, 3, 2},
		{"Qatar", "Qatar", 58, "Lusail Stadium", 25.4209, 51.4900, 5, false, "Hassan Al-Haydos", 180, "Meshaal Barsham", 35, 6.8, 0, 0},
		{"Saudi Arabia", "Saudi Arabia", 56, "King Fahd Stadium", 24.7895, 46.8395, 612, false, "Salman Al-Faraj", 75, "Mohammed Al-Owais", 45, 6.9, 0, 0},
		{"Scotland", "Scotland", 37, "Hampden Park", 55.8258, -4.2520, 25, false, "Andy Robertson", 75, "Angus Gunn", 20, 7.0, 0, 0},
		{"Senegal", "Senegal", 17, "Stade Abdoulaye Wade", 14.7529, -17.1947, 24, false, "Kalidou Koulibaly", 80, "Edouard Mendy", 35, 7.5, 0, 0},
		{"South Africa", "South Africa", 59, "FNB Stadium", -26.2348, 27.9827, 1753, false, "Ronwen Williams", 45, "Ronwen Williams", 45, 7.1, 0, 0},
		{"Spain", "Spain", 3, "Santiago Bernabeu", 40.4531, -3.6883, 667, false, "Alvaro Morata", 80, "Unai Simon", 45, 7.6, 4, 2},
		{"Sweden", "Sweden", 27, "Friends Arena", 59.3726, 18.0003, 35, false, "Victor Lindelof", 70, "Robin Olsen", 75, 7.0, 1, 1},
		{"Switzerland", "Switzerland", 19, "St. Jakob-Park", 47.5415, 7.6206, 260, false, "Granit Xhaka", 125, "Yann Sommer", 90, 7.8, 1, 1},
		{"Tunisia", "Tunisia", 41, "Stade Olympique de Rades", 36.7478, 10.2728, 15, false, "Youssef Msakni", 100, "Aymen Dahmen", 20, 6.8, 0, 0},
		{"Turkiye", "Turkey", 26, "Ataturk Olympic Stadium", 41.0745, 28.7657, 105, false, "Hakan Calhanoglu", 90, "Ugurcan Cakir", 30, 7.2, 0, 1},
		{"Uruguay", "Uruguay", 11, "Estadio Centenario", -34.8944, -56.1522, 43, false, "Federico Valverde", 65, "Sergio Rochet", 25, 7.3, 2, 2},
		{"USA", "USA", 13, "Mercedes-Benz Stadium", 33.7554, -84.4008, 320, true, "Christian Pulisic", 75, "Matt Turner", 45, 7.2, 1, 1},
		{"Uzbekistan", "Uzbekistan", 57, "Milliy Stadium", 41.2857, 69.2048, 455, false, "Eldor Shomurodov", 75, "Utkir Yusupov", 35, 6.9, 0, 0},
	}
}

func staticRank(name string) int {
	for _, t := range staticTeams() {
		if strings.EqualFold(t.Name, name) {
			return t.FIFARank
		}
	}
	return 40
}

func averageAge(players []struct {
	Name     string
	Age      int
	Position string
}) float64 {
	var total, count float64
	for _, p := range players {
		if p.Age > 0 {
			total += float64(p.Age)
			count++
		}
	}
	if count == 0 {
		return 27
	}
	return total / count
}

func squadDepth(players []struct {
	Name     string
	Age      int
	Position string
}) float64 {
	counts := map[string]int{}
	for _, p := range players {
		counts[p.Position]++
	}
	score := 4.0
	for _, pos := range []string{"Goalkeeper", "Defender", "Midfielder", "Attacker"} {
		if counts[pos] >= 2 {
			score += 1.2
		}
	}
	return clamp(score, 1, 10)
}

func sameClubFallback(team string) float64 {
	switch strings.ToLower(team) {
	case "spain", "germany", "england":
		return 2.5
	case "argentina", "brazil", "france":
		return 1.5
	default:
		return 1.0
	}
}

func ageScore(avg float64) float64 {
	if avg >= 26 && avg <= 29 {
		return 10
	}
	if avg < 23 {
		return clamp(6-(23-avg)*1.2, 1, 10)
	}
	if avg > 32 {
		return clamp(6-(avg-32)*1.2, 1, 10)
	}
	return 8
}

func dataCompleteness(m TeamMetrics) float64 {
	total := 12.0
	missing := float64(len(m.ExcludedFactors))
	return clamp((total-missing)/total, .35, 1)
}

func weatherSummary(w Weather) string {
	parts := []string{fmt.Sprintf("%.0f°C", w.TempC), fmt.Sprintf("%.0f%% humidity", w.HumidityPct), fmt.Sprintf("%.0fkm/h wind", w.WindKMH)}
	if w.RainMM > 0 {
		parts = append(parts, fmt.Sprintf("%.1fmm rain", w.RainMM))
	}
	return strings.Join(parts, ", ")
}

func uniqueJoined(items []string) string {
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func closestNation(input string) string {
	target := normalizeName(input)
	best, bestScore := worldCupNations[0], math.MaxInt
	for _, n := range worldCupNations {
		score := levenshtein(target, normalizeName(n))
		if score < bestScore {
			best, bestScore = n, score
		}
	}
	return best
}

func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, "’", "'")
	s = strings.ReplaceAll(s, "é", "e")
	s = strings.ReplaceAll(s, "ô", "o")
	s = strings.ReplaceAll(s, "ç", "c")
	s = strings.ReplaceAll(s, "ü", "u")
	s = strings.ReplaceAll(s, "ï", "i")
	s = strings.ReplaceAll(s, "united states", "usa")
	s = strings.ReplaceAll(s, "u s a", "usa")
	s = strings.ReplaceAll(s, "cote d'ivoire", "ivory coast")
	s = strings.ReplaceAll(s, "côte d'ivoire", "ivory coast")
	s = strings.ReplaceAll(s, "cape verde", "cabo verde")
	s = strings.ReplaceAll(s, "dr congo", "congo dr")
	s = strings.ReplaceAll(s, "democratic republic of congo", "congo dr")
	s = strings.ReplaceAll(s, "ir iran", "iran")
	s = strings.ReplaceAll(s, "islamic republic of iran", "iran")
	s = strings.ReplaceAll(s, "south korea", "korea republic")
	s = strings.ReplaceAll(s, "republic of korea", "korea republic")
	s = strings.ReplaceAll(s, "turkiye", "turkey")
	s = strings.ReplaceAll(s, "türkiye", "turkey")
	s = strings.ReplaceAll(s, "curacao", "curacao")
	s = strings.Join(strings.Fields(s), " ")
	return s
}

func levenshtein(a, b string) int {
	da := []rune(a)
	db := []rune(b)
	dp := make([][]int, len(da)+1)
	for i := range dp {
		dp[i] = make([]int, len(db)+1)
	}
	for i := 0; i <= len(da); i++ {
		dp[i][0] = i
	}
	for j := 0; j <= len(db); j++ {
		dp[0][j] = j
	}
	for i := 1; i <= len(da); i++ {
		for j := 1; j <= len(db); j++ {
			cost := 0
			if da[i-1] != db[j-1] {
				cost = 1
			}
			dp[i][j] = min(dp[i-1][j]+1, dp[i][j-1]+1, dp[i-1][j-1]+cost)
		}
	}
	return dp[len(da)][len(db)]
}

func urlQuery(s string) string {
	return url.QueryEscape(strings.TrimSpace(s))
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
