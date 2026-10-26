package nlu

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"skarb/internal/domain"
	"skarb/internal/service"
)

var (
	segmentSplitRE = regexp.MustCompile(`[,;\n\r]|\s+(?:та|і|and)\s+`)
	decimalCommaRE = regexp.MustCompile(`(\d),(\d{1,2})\b`)
	amountRE       = regexp.MustCompile(`(?i)(\d{1,3}(?:[ ]\d{3})+(?:[.,]\d{1,2})?|\d+(?:[.,]\d{1,2})?)\s*(грн|₴|uah|usd|долар[а-яіїєґ']*|\$|eur|євро|€|pln|zł|gbp|£|chf)?`)
	explicitDateRE = regexp.MustCompile(`\b\d{1,2}[./]\d{1,2}(?:[./]\d{2,4})?\b`)
	relativeDateRE = regexp.MustCompile(`(?i)(\d+|один|одна|два|дві|три|чотири|п'ять|пять|шість|сім|вісім|дев'ять|десять|one|two|three|four|five|six|seven|eight|nine|ten)\s*(день|дні|днів|дня|доби|діб|day|days)\s*(тому|назад|ago)`)
)

var wordNumbers = map[string]int{
	"один": 1, "одна": 1, "one": 1,
	"два": 2, "дві": 2, "two": 2,
	"три": 3, "three": 3,
	"чотири": 4, "four": 4,
	"п'ять": 5, "пять": 5, "five": 5,
	"шість": 6, "six": 6,
	"сім": 7, "seven": 7,
	"вісім": 8, "eight": 8,
	"дев'ять": 9, "nine": 9,
	"десять": 10, "ten": 10,
}

func relativeDays(text string) (int, bool) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "позавчора"):
		return 2, true
	case strings.Contains(lower, "вчора"), strings.Contains(lower, "учора"), strings.Contains(lower, "yesterday"):
		return 1, true
	case strings.Contains(lower, "сьогодні"), strings.Contains(lower, "today"):
		return 0, true
	case strings.Contains(lower, "тиждень тому"), strings.Contains(lower, "a week ago"), strings.Contains(lower, "one week ago"):
		return 7, true
	case strings.Contains(lower, "день тому"), strings.Contains(lower, "a day ago"):
		return 1, true
	}
	if m := relativeDateRE.FindStringSubmatch(lower); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n, true
		}
		if n, ok := wordNumbers[m[1]]; ok {
			return n, true
		}
	}
	return 0, false
}

func (p *Parser) ParseRegex(ctx context.Context, text string) (*Intent, error) {
	clean := strings.ReplaceAll(text, " ", " ")
	clean = strings.ReplaceAll(clean, " ", " ")
	// A comma between digits is a decimal separator, not a transaction delimiter.
	clean = decimalCommaRE.ReplaceAllString(clean, "${1}.${2}")
	today := p.svc.Today()
	cats, err := p.svc.Store.ListCategories(ctx, false)
	if err != nil {
		return nil, err
	}
	date := regexDate(clean, today)
	segments := segmentSplitRE.Split(clean, -1)
	intent := &Intent{Operation: "add_transactions", Confidence: 0.55}
	type segmentMatches struct {
		segment string
		matches [][]string
	}
	var withAmounts []segmentMatches
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		withoutDate := explicitDateRE.ReplaceAllString(segment, " ")
		withoutDate = relativeDateRE.ReplaceAllString(withoutDate, " ")
		matches := amountRE.FindAllStringSubmatch(withoutDate, -1)
		if len(matches) == 0 {
			continue
		}
		withAmounts = append(withAmounts, segmentMatches{segment: segment, matches: matches})
	}
	for _, sm := range withAmounts {
		category := matchCategory(sm.segment, cats)
		if category == nil && len(withAmounts) == 1 {
			// A single amount may rely on a category named elsewhere in the
			// message. With several amount lines, that would bleed one line's
			// category onto another.
			category = matchCategory(clean, cats)
		}
		catName := "Інше"
		if category != nil {
			catName = category.Name
		}
		for _, match := range sm.matches {
			amount := strings.TrimSpace(match[1])
			if amount == "" {
				continue
			}
			currency := currencyFromToken(match[2])
			description := descriptionFromSegment(sm.segment, catName)
			intent.Transactions = append(intent.Transactions, ParsedTransaction{
				Amount:      amount,
				Currency:    currency,
				Date:        date.String(),
				Category:    catName,
				Description: description,
			})
			if len(intent.Transactions) >= 10 {
				break
			}
		}
		if len(intent.Transactions) >= 10 {
			break
		}
	}
	if len(intent.Transactions) == 0 {
		return nil, ErrNotUnderstood
	}
	if err := p.validate(ctx, intent, serviceDefaults()); err != nil {
		return nil, err
	}
	return intent, nil
}

func regexDate(text string, today domain.Date) domain.Date {
	if days, ok := relativeDays(text); ok {
		return today.AddDays(-days)
	}
	if m := explicitDateRE.FindString(text); m != "" {
		layouts := []string{"2006-01-02", "02.01.2006", "2.1.2006", "02/01/2006", "2/1/2006"}
		for _, layout := range layouts {
			if t, err := parseDateLayout(m, layout); err == nil {
				return domain.DateOf(t)
			}
		}
	}
	return today
}

func matchCategory(text string, cats []domain.Category) *domain.Category {
	lower := strings.ToLower(text)
	var best *domain.Category
	bestLen := 0
	for i := range cats {
		c := &cats[i]
		if len([]rune(c.Name)) >= 2 && strings.Contains(lower, strings.ToLower(c.Name)) && len([]rune(c.Name)) > bestLen {
			best = c
			bestLen = len([]rune(c.Name))
		}
		for _, alias := range c.Aliases {
			aliasLower := strings.ToLower(alias)
			if len([]rune(aliasLower)) >= 2 && strings.Contains(lower, aliasLower) && len([]rune(aliasLower)) > bestLen {
				best = c
				bestLen = len([]rune(aliasLower))
			}
		}
	}
	return best
}

func currencyFromToken(token string) string {
	token = strings.ToLower(strings.TrimSpace(token))
	switch {
	case token == "", token == "грн", token == "₴", token == "uah":
		return "UAH"
	case token == "$", token == "usd", strings.HasPrefix(token, "долар"):
		return "USD"
	case token == "€", token == "eur", strings.Contains(token, "євро"):
		return "EUR"
	case token == "zł", token == "pln":
		return "PLN"
	case token == "£", token == "gbp":
		return "GBP"
	case token == "chf":
		return "CHF"
	default:
		return "UAH"
	}
}

func descriptionFromSegment(segment, fallback string) string {
	lower := strings.ToLower(segment)
	for _, marker := range []string{" за ", " на ", " for "} {
		if i := strings.Index(lower, marker); i >= 0 {
			desc := strings.TrimSpace(segment[i+len(marker):])
			desc = strings.Trim(desc, " .,;:!?")
			if desc != "" {
				return desc
			}
		}
	}
	return fallback
}

func serviceDefaults() service.LLMConfig {
	return service.DefaultLLMConfig()
}

func parseDateLayout(s, layout string) (time.Time, error) {
	return time.Parse(layout, s)
}
