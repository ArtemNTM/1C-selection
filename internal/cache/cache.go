package cache

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/bochkarevart/1c-selection/internal/model"
)

type Source func(context.Context) (Snapshot, error)

type Snapshot struct {
	Clients             []model.Client
	Stories             []model.Story
	AsOf                time.Time
	UncacheableStoryIDs []int64
}

type Cache struct {
	source   Source
	halfLife time.Duration
	now      func() time.Time

	mu              sync.RWMutex
	refreshMu       sync.Mutex
	clients         map[int64]model.Client
	stories         map[int64]model.Story
	asOf            time.Time
	clientsComplete bool
	storiesComplete bool
	generation      uint64
}

func New(source Source, halfLife time.Duration) *Cache {
	return NewWithClock(source, halfLife, time.Now)
}

func NewWithClock(source Source, halfLife time.Duration, now func() time.Time) *Cache {
	if now == nil {
		now = time.Now
	}
	return &Cache{
		source:   source,
		halfLife: halfLife,
		now:      now,
		clients:  make(map[int64]model.Client),
		stories:  make(map[int64]model.Story),
	}
}

func (c *Cache) Run(ctx context.Context) error {
	return c.Refresh(ctx)
}

func (c *Cache) Refresh(ctx context.Context) error {
	if c.source == nil {
		return nil
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.mu.RLock()
	generation := c.generation
	c.mu.RUnlock()

	snapshot, err := c.source(ctx)
	if err != nil {
		return err
	}
	clients := make(map[int64]model.Client, len(snapshot.Clients))
	for _, client := range snapshot.Clients {
		clients[client.ID] = client
	}
	stories := make(map[int64]model.Story, len(snapshot.Stories))
	uncacheable := make(map[int64]struct{}, len(snapshot.UncacheableStoryIDs))
	for _, id := range snapshot.UncacheableStoryIDs {
		uncacheable[id] = struct{}{}
	}
	for _, story := range snapshot.Stories {
		if _, skip := uncacheable[story.ID]; skip {
			continue
		}
		stories[story.ID] = cloneStory(story)
	}
	asOf := snapshot.AsOf.UTC()
	if asOf.IsZero() {
		asOf = c.now().UTC()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		return nil
	}
	c.clients, c.stories, c.asOf = clients, stories, asOf
	c.clientsComplete, c.storiesComplete = true, len(uncacheable) == 0
	return nil
}

func (c *Cache) InvalidateClients() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.clientsComplete = false
	c.clients = make(map[int64]model.Client)
}

func (c *Cache) InvalidateStories() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.storiesComplete = false
	c.stories = make(map[int64]model.Story)
}

func (c *Cache) InvalidateStory(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.storiesComplete = false
	delete(c.stories, id)
}

func (c *Cache) Client(id int64) (model.Client, bool) {
	c.mu.RLock()
	client, ok := c.clients[id]
	c.mu.RUnlock()
	return client, ok
}

func (c *Cache) Clients() ([]model.Client, bool) {
	c.mu.RLock()
	if !c.clientsComplete {
		c.mu.RUnlock()
		return nil, false
	}
	result := make([]model.Client, 0, len(c.clients))
	for _, client := range c.clients {
		result = append(result, client)
	}
	c.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result, true
}

func (c *Cache) Story(id int64) (model.Story, bool) {
	c.mu.RLock()
	story, ok := c.stories[id]
	asOf := c.asOf
	c.mu.RUnlock()
	if !ok {
		return model.Story{}, false
	}
	return c.materializeStory(story, asOf), true
}

func (c *Cache) Stories(limit int) ([]model.Story, bool) {
	c.mu.RLock()
	if limit < 1 || limit > 1000 || !c.storiesComplete {
		c.mu.RUnlock()
		return nil, false
	}
	stories := make([]model.Story, 0, len(c.stories))
	asOf := c.asOf
	for _, story := range c.stories {
		stories = append(stories, story)
	}
	c.mu.RUnlock()
	sort.Slice(stories, func(i, j int) bool {
		return stories[i].ID > stories[j].ID
	})
	if limit < len(stories) {
		stories = stories[:limit]
	}
	for i := range stories {
		stories[i].Participants = nil
		stories[i] = c.materializeStory(stories[i], asOf)
	}
	return stories, true
}

func (c *Cache) materializeStory(story model.Story, asOf time.Time) model.Story {
	result := cloneStory(story)
	if result.TrustAtPublish == nil && c.halfLife > 0 {
		age := c.now().UTC().Sub(asOf)
		if age > 0 {
			result.CurrentTrust *= math.Exp2(-float64(age) / float64(c.halfLife))
		}
	}
	return result
}

func cloneStory(in model.Story) model.Story {
	out := in
	if in.PublishedAt != nil {
		value := *in.PublishedAt
		out.PublishedAt = &value
	}
	if in.TrustAtPublish != nil {
		value := *in.TrustAtPublish
		out.TrustAtPublish = &value
	}
	if in.Participants != nil {
		out.Participants = make([]model.Participant, len(in.Participants))
		copy(out.Participants, in.Participants)
		for i := range out.Participants {
			if in.Participants[i].Rank != nil {
				value := *in.Participants[i].Rank
				out.Participants[i].Rank = &value
			}
		}
	}
	return out
}
