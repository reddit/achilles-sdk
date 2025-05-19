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
		success      bool
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
			name: "get (success=true)",
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
			success: true,
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
			name: "get (success=false)",
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
					Time:       time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
					Failed:     true,
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 2,
					Time:       time.Date(2000, 2, 1, 0, 0, 0, 0, time.UTC),
					Failed:     true,
				},
				{
					// match
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 3,
					Time:       time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC),
				},
				{
					// match
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 4,
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
			success: false,
			getRange: requestStartTime{
				Name:       "bbb",
				Namespace:  "ns",
				Generation: 4,
			},
			expectedItem: []time.Time{
				time.Date(2000, 4, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProcessingStartTimes()

			for _, item := range tc.addItems {
				p.startTimes.ReplaceOrInsert(requestStartTime{
					Namespace:  item.Namespace,
					Name:       item.Name,
					Generation: item.Generation,
					Time:       item.Time,
					Failed:     item.Failed,
				})
			}

			got := p.GetRange(tc.getRange.Name, tc.getRange.Namespace, tc.getRange.Generation, tc.success)

			// order matters
			assert.Equal(t, tc.expectedItem, got)
		})
	}
}

func Test_ProcessingStartTimes_Set(t *testing.T) {
	tests := []struct {
		name         string
		addItems     []requestStartTime
		expectedTree []requestStartTime
	}{
		{
			name:         "no items",
			addItems:     []requestStartTime{},
			expectedTree: nil,
		},
		{
			name:         "no items with delete",
			addItems:     []requestStartTime{},
			expectedTree: nil,
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProcessingStartTimes()

			for _, item := range tc.addItems {
				p.Set(item.Name, item.Namespace, item.Generation, item.Time)
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

func Test_ProcessingStartTimes_DeleteRange(t *testing.T) {
	tests := []struct {
		name         string
		addItems     []requestStartTime
		deleteRanges []requestStartTime // executed after all adds
		expectedTree []requestStartTime
	}{
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
			name: "deletes",
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
				p.startTimes.ReplaceOrInsert(requestStartTime{
					Namespace:  item.Namespace,
					Name:       item.Name,
					Generation: item.Generation,
					Time:       item.Time,
					Failed:     item.Failed,
				})
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

func Test_ProcessingStartTimes_SetRangeFailed(t *testing.T) {
	tests := []struct {
		name         string
		addItems     []requestStartTime
		failedRanges []requestStartTime // executed after all adds
		expectedTree []requestStartTime
	}{
		{
			name: "set range failed",
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
					// NOTE: this should be failed but don't set it to allow testing early exit
				},
				{
					Name:       "ccc",
					Namespace:  "ns",
					Generation: 2,
					Time:       time.Date(2000, 5, 1, 0, 0, 0, 0, time.UTC),
					Failed:     true,
				},
				{
					Name:       "ccc",
					Namespace:  "ns",
					Generation: 3,
					Time:       time.Date(2000, 5, 1, 0, 0, 0, 0, time.UTC),
				},
			},
			failedRanges: []requestStartTime{
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
					Generation: 1,
					Time:       time.Date(2000, 2, 1, 0, 0, 0, 0, time.UTC),
					Failed:     true,
				},
				{
					Name:       "bbb",
					Namespace:  "ns",
					Generation: 2,
					Time:       time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC),
					Failed:     true,
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
				{
					Name:       "ccc",
					Namespace:  "ns",
					Generation: 2,
					Time:       time.Date(2000, 5, 1, 0, 0, 0, 0, time.UTC),
					Failed:     true,
				},
				{
					Name:       "ccc",
					Namespace:  "ns",
					Generation: 3,
					Time:       time.Date(2000, 5, 1, 0, 0, 0, 0, time.UTC),
					Failed:     true,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProcessingStartTimes()

			for _, item := range tc.addItems {
				p.startTimes.ReplaceOrInsert(requestStartTime{
					Namespace:  item.Namespace,
					Name:       item.Name,
					Generation: item.Generation,
					Time:       item.Time,
					Failed:     item.Failed,
				})
			}

			for _, item := range tc.failedRanges {
				p.SetRangeFailed(item.Name, item.Namespace, item.Generation)
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
