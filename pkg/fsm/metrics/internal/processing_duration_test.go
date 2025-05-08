package internal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_ProcessingStartTimes_Read(t *testing.T) {
	tests := []struct {
		name     string
		addItems []requestStartTime

		getRange     requestStartTime
		expectedItem []time.Time
	}{
		{
			name:     "no items",
			addItems: []requestStartTime{},
			getRange: requestStartTime{
				Name:       "bbb",
				Namespace:  "ns",
				Generation: 1,
			},
			expectedItem: nil,
		},
		{
			name: "adds",
			addItems: []requestStartTime{
				{
					Name:       "aaa",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					// match
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 2, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					// match
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 2,
					Time:       time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 3,
					Time:       time.Date(2000, 4, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "bb",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "bbb",
					Namespace:  "n",
					Generation: 1,
					Time:       time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "ccc",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 5, 1, 0, 0, 0, 0, time.UTC),
				},
			},
			getRange: requestStartTime{
				Name:       "bbb",
				Namespace:  "ns",
				Generation: 2,
			},
			expectedItem: []time.Time{
				time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2000, 2, 1, 0, 0, 0, 0, time.UTC),
			},
		},
		{
			name: "adds with item replacement",
			addItems: []requestStartTime{
				{
					Name:       "aaa",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					// item should replace previous item
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 2, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					// item should replace previous item
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 3,
					Time:       time.Date(2000, 4, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "ccc",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 5, 1, 0, 0, 0, 0, time.UTC),
				},
			},
			getRange: requestStartTime{
				Name:       "bbb",
				Namespace:  "ns",
				Generation: 1,
			},
			expectedItem: []time.Time{
				time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProcessingStartTimes()

			for _, item := range tc.addItems {
				p.SetIfEarliest(item.Name, item.Namespace, item.Generation, item.Time)
			}

			got := p.GetRange(tc.getRange.Name, tc.getRange.Namespace, tc.getRange.Generation)

			// order matters
			assert.Equal(t, tc.expectedItem, got)
		})
	}
}

func Test_ProcessingStartTimes_Write(t *testing.T) {
	tests := []struct {
		name         string
		addItems     []requestStartTime
		deleteRanges []requestStartTime // executed after all adds
		expectedTree []requestStartTime
	}{
		{
			name:         "no items",
			addItems:     []requestStartTime{},
			deleteRanges: []requestStartTime{},
			expectedTree: nil,
		},
		{
			name:     "no items with delete",
			addItems: []requestStartTime{},
			deleteRanges: []requestStartTime{
				{
					Name:       "aaa",
					Namespace:  "ns",
					Generation: 1,
				},
			},
			expectedTree: nil,
		},
		{
			name: "adds with no delete",
			addItems: []requestStartTime{
				{
					Name:       "aaa",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					// match
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 2, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					// match
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 2,
					Time:       time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 3,
					Time:       time.Date(2000, 4, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "ccc",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 5, 1, 0, 0, 0, 0, time.UTC),
				},
			},
			expectedTree: []requestStartTime{
				{
					Name:       "aaa",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					// match
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 2, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					// match
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 2,
					Time:       time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 3,
					Time:       time.Date(2000, 4, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "ccc",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 5, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			name: "adds with deletes",
			addItems: []requestStartTime{
				{
					Name:       "aaa",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 2, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 2,
					Time:       time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 3,
					Time:       time.Date(2000, 4, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "ccc",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 5, 1, 0, 0, 0, 0, time.UTC),
				},
			},
			deleteRanges: []requestStartTime{
				{
					Name:       "ccc",
					Namespace:  "ns",
					Generation: 99,
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 2,
				},
			},
			expectedTree: []requestStartTime{
				{
					Name:       "aaa",
					Namespace:  "ns",
					Generation: 1,
					Time:       time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 3,
					Time:       time.Date(2000, 4, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProcessingStartTimes()

			for _, item := range tc.addItems {
				p.SetIfEarliest(item.Name, item.Namespace, item.Generation, item.Time)
			}

			for _, item := range tc.deleteRanges {
				p.DeleteRange(item.Name, item.Namespace, item.Generation)
			}

			var got []requestStartTime
			p.startTimes.Ascend(func(item requestStartTime) bool {
				got = append(got, item)
				return true
			})

			// order matters
			assert.Equal(t, tc.expectedTree, got)
		})
	}
}
