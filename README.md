# 🔮 Nostradamus - World Cup 2026 Match Predictor

A CLI tool written in Go that predicts World Cup 2026 match outcomes using real-time data from API-Football and Open-Meteo.

## Features
- Live fixture listings for the next 7 days
- Real team form, lineups, and statistics
- Weather impact on predictions
- Home/host nation boost
- Tournament stage modifiers

## Usage
```bash
# List upcoming fixtures
go run main.go --list-fixtures

# Predict by fixture ID
go run main.go --fixture 1489384

# Manual mode
go run main.go "brazil" "argentina"
```

## Setup
```bash
export API_FOOTBALL_KEY="your_key_here"
go run main.go --list-fixtures
```

## Disclaimer
⚠️ This model knows nothing. Enjoy responsibly.
