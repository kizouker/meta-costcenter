package costcenter

import (
	"testing"
	"time"
)

func TestChartResolutions(t *testing.T) {
	saved := now
	defer func() { now = saved }()
	noon := time.Date(2026, 10, 27, 12, 7, 0, 0, time.Local)
	now = func() time.Time { return noon }
	at := func(hour, minute int) string {
		return isoformat(time.Date(2026, 10, 27, hour, minute, 0, 0, time.Local))
	}
	rows := []Row{{RecordedAt: at(9, 2), Input: 10}, {RecordedAt: at(9, 14), Input: 5}, {RecordedAt: at(9, 15), Input: 7},
		{RecordedAt: at(11, 59), Input: 1}}

	// Today defaults to 15-minute blocks, with every block of the day on the axis.
	c, err := chart(rows, nil, Filters{Period: "today"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Unit != "15m" || len(c.Points) != 96 || c.Current != "2026-10-27T12:00" {
		t.Fatalf("unit %s, %d points, current %s", c.Unit, len(c.Points), c.Current)
	}
	if p := c.Points[36]; p.Bucket != "2026-10-27T09:00" || p.Input != 15 {
		t.Fatalf("09:00 block %+v", p)
	}
	if p := c.Points[37]; p.Bucket != "2026-10-27T09:15" || p.Input != 7 {
		t.Fatalf("09:15 block %+v", p)
	}
	if p := c.Points[0]; p.Bucket != "2026-10-27T00:00" || p.Input != 0 {
		t.Fatalf("empty block %+v", p)
	}

	c, _ = chart(rows, nil, Filters{Period: "today"}, "hour")
	if c.Unit != "hour" || len(c.Points) != 24 || c.Points[9].Input != 22 || c.Current != "2026-10-27T12:00" {
		t.Fatalf("hourly %s %d %+v", c.Unit, len(c.Points), c.Points[9])
	}

	// A week defaults to hours and is too long for 15-minute blocks.
	c, _ = chart(rows, nil, Filters{Period: "week"}, "15m")
	if c.Unit != "hour" || len(c.Points) != 7*24 || len(c.Units) != 2 {
		t.Fatalf("week %s %d %v", c.Unit, len(c.Points), c.Units)
	}

	// All time starts at the first usage and is drawn by day.
	c, _ = chart(rows, nil, Filters{Period: "all"}, "")
	if c.Unit != "15m" || len(c.Points) != 96 {
		t.Fatalf("all time %s %d", c.Unit, len(c.Points))
	}
	c, _ = chart(rows, nil, Filters{Period: "month"}, "")
	if c.Unit != "day" || len(c.Points) != 27 || c.Points[26].Input != 23 || c.Current != "2026-10-27" {
		t.Fatalf("month %s %d %+v", c.Unit, len(c.Points), c.Points[26])
	}
}
