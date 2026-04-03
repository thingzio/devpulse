package health

import "math"

type DemandInput struct {
	StarGrowthPct            float64
	ExternalContributorDelta float64
	NewPRDelta               float64
	NewIssueDelta            float64
}

type ThroughputInput struct {
	MedianMergeHours float64
	PRBacklogDelta   float64
	AgingPRsPct      float64
}

type ResponsivenessInput struct {
	FirstResponsePRHours    float64
	FirstResponseIssueHours float64
	RespondedWithin48hPct   float64
	UnansweredPct           float64
}

func Grade(score float64) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 70:
		return "B"
	case score >= 50:
		return "C"
	case score >= 30:
		return "D"
	default:
		return "F"
	}
}

func DemandScore(d DemandInput) float64 {
	s := subScore(d.StarGrowthPct, 10) +
		subScore(d.ExternalContributorDelta, 5) +
		subScore(d.NewPRDelta, 10) +
		subScore(d.NewIssueDelta, 10)
	return clamp(s)
}

func ThroughputScore(t ThroughputInput) float64 {
	mergeScore := inverseScore(t.MedianMergeHours, 168)
	agingScore := inverseScore(t.AgingPRsPct, 100)
	backlogScore := inverseScore(math.Max(t.PRBacklogDelta, 0), 20)
	return clamp((mergeScore + agingScore + backlogScore) / 3)
}

func ResponsivenessScore(r ResponsivenessInput) float64 {
	prResponse := inverseScore(r.FirstResponsePRHours, 168)
	issueResponse := inverseScore(r.FirstResponseIssueHours, 168)
	sloScore := r.RespondedWithin48hPct
	unansweredScore := inverseScore(r.UnansweredPct, 100)
	return clamp((prResponse + issueResponse + sloScore + unansweredScore) / 4)
}

func Overall(demand, throughput, responsiveness float64) float64 {
	return clamp((demand + throughput + responsiveness) / 3)
}

func subScore(delta, excellent float64) float64 {
	if delta >= excellent {
		return 25
	}
	if delta <= 0 {
		ratio := math.Max(delta/excellent, -1)
		return 12.5 + ratio*12.5
	}
	return 12.5 + (delta/excellent)*12.5
}

func inverseScore(val, worst float64) float64 {
	if val <= 0 {
		return 100
	}
	if val >= worst {
		return 0
	}
	return 100 * (1 - val/worst)
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
