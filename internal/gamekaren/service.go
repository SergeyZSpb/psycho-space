package gamekaren

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/SergeyZSpb/psycho-space/internal/db"
	"github.com/SergeyZSpb/psycho-space/internal/realtime"
	"github.com/google/uuid"
)

// The service: the office, the loop that advances it, and the one goroutine that
// writes a finished shift down.
//
// Copied in SHAPE from «ВАНЯДУМ» — the injected tick, the buffered saves
// channel, the drain on shutdown, the build-under-lock/publish-after — and in
// nothing else. Per ADR-028 the two games share no code, and the duplication is
// deliberate rather than debt.

// DefaultShiftLimit is how many rows a list returns when the caller does not
// say, and MaxShiftLimit is the most it will ever return. Both bound a query a
// client controls; neither is on the wire.
const (
	DefaultShiftLimit = 10
	MaxShiftLimit     = 50
)

// savesBuffer is how many finished shifts may be waiting to be written before
// the loop starts dropping them. Far more than a floor of three people can
// produce.
const savesBuffer = 64

// pseudonymBytes is how much of the HMAC becomes a handle: nine bytes, which is
// twelve base64url characters.
//
// It is a display handle rather than a secret, so the bar is collision
// resistance across an office of three — nine bytes is vastly past that. What it
// must NOT be is short enough to enumerate back to an account, which is why it
// is not four.
const pseudonymBytes = 9

// Transport is the narrow slice of the realtime hub this game uses. It is an
// interface so the tests drive the whole service without a socket, and it is
// deliberately two methods wide: this game addresses its frames to connections
// and never broadcasts, because two people in one office get two different
// snapshots.
type Transport interface {
	// PublishTo sends to a single connection. An unknown connection id is a
	// no-op rather than an error — a socket may go away between the tick that
	// named it and the write.
	PublishTo(ctx context.Context, connID string, msg []byte) error
	// Members reports who is connected to a room.
	Members(ctx context.Context, room string) ([]realtime.Member, error)
}

// Profiles is what this game needs from the account service and nothing more:
// the picture to draw on somebody's Карен.
//
// One method rather than the whole account, because everything else on one is
// personal data this package has no business holding. Re-declared here rather
// than shared with «Ванягоччи» — a game owns its own dependencies (ADR-028), and
// the two happen to want the same thing.
//
// Nil is a supported state: without one, everybody is a plain figure, which is
// exactly what the office looked like before avatars.
type Profiles interface {
	// AvatarURL returns the account's avatar, or "" when it has none.
	AvatarURL(ctx context.Context, accountID string) (string, error)
}

// Service owns the office and the simulation loop that advances it.
type Service struct {
	transport Transport
	room      string
	q         db.DBTX
	repo      Repository
	profiles  Profiles
	// pseudonymKey turns an account id into the handle other occupants see.
	// Read-only after construction, so it needs no lock. See pseudonym.
	pseudonymKey []byte

	mu sync.Mutex
	// office is nil when nobody is working. It is built by the first StartShift
	// and dropped when the last person leaves, so an idle process simulates
	// nothing at all and the bald man is always back at the far wall for the
	// first person in.
	office *Office

	// saves carries finished shifts to the writer goroutine, so the tick never
	// waits on Postgres. A full channel drops the shift and says so in the log:
	// stalling a twenty-hertz simulation for everybody in order to record one
	// summary row is the wrong trade, and the log line is what stops the loss
	// being silent.
	saves chan Shift
	done  chan struct{}
}

// NewService builds the game.
func NewService(t Transport, room string, q db.DBTX, repo Repository, profiles Profiles) *Service {
	return &Service{
		transport:    t,
		room:         room,
		q:            q,
		repo:         repo,
		profiles:     profiles,
		saves:        make(chan Shift, savesBuffer),
		done:         make(chan struct{}),
		pseudonymKey: randomKey(),
	}
}

// randomKey mints the per-process key the pseudonyms are derived from.
func randomKey() []byte {
	k := make([]byte, 32)
	// crypto/rand.Read is documented never to fail, and panics internally if the
	// system source is broken — so there is nothing to handle here that has not
	// already brought the process down.
	_, _ = rand.Read(k)
	return k
}

// pseudonym is the handle an account is known by to the OTHER occupants:
// HMAC-SHA256(process key, account id), base64url, truncated.
//
// This is ADR-037. An account id is a durable identifier for a person and it
// never reaches another player's browser; the key is minted at startup and held
// only in memory, so a handle is stable for exactly as long as the office it
// describes and means nothing after a restart — which is the same lifetime the
// office itself has, and therefore costs nothing.
//
// Re-derived per call rather than cached: it is one HMAC, it happens on join,
// and a map from account to handle would be a second place for the truth to live.
func (s *Service) pseudonym(accountID string) string {
	mac := hmac.New(sha256.New, s.pseudonymKey)
	_, _ = mac.Write([]byte(accountID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:pseudonymBytes])
}

// AvatarFor is the picture to draw on a peer, by the handle the frame named him
// with. Nothing when nobody is working, which is when there are no peers either.
func (s *Service) AvatarFor(handle string) (string, bool) {
	s.mu.Lock()
	office := s.office
	s.mu.Unlock()
	if office == nil {
		return "", false
	}
	return office.AvatarFor(handle)
}

// Done is closed once Run has returned and the last write has been attempted, so
// the composition root can wait for it during shutdown. Skipping that wait
// silently loses the final flush.
func (s *Service) Done() <-chan struct{} { return s.done }

// Config is the served catalogue.
func (s *Service) Config() Config { return BuildConfig() }

// Run advances the office on the injected tick.
//
// The tick is a parameter rather than a ticker built here (ADR-034's rule,
// applied again): main passes a time.Ticker, and every test passes a channel it
// fires by hand — which is what keeps the simulation tests free of sleeps and
// makes "advance exactly one hundred steps" a thing a test can say.
func (s *Service) Run(ctx context.Context, tick <-chan time.Time) {
	defer close(s.done)
	go s.persistShifts(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick:
			s.step(ctx, now)
		}
	}
}

// outbound is one frame waiting for the lock to be released.
type outbound struct {
	connID string
	msg    []byte
}

// step is one simulation tick.
//
// The publishing is deliberately done AFTER the lock is released. PublishTo
// hands the message to the hub's own goroutine, and holding this mutex across
// that would couple the simulation to the hub's queue — so the locked section
// only ever builds bytes.
func (s *Service) step(ctx context.Context, now time.Time) {
	conns := s.connsByAccount(ctx)

	s.mu.Lock()
	office := s.office
	if office == nil {
		s.mu.Unlock()
		return
	}

	// Everybody with a connection is present, whether or not they sent
	// anything. A player standing perfectly still sends nothing at all, and is
	// the most present person in the game.
	for account := range conns {
		office.Seen(account, now)
	}

	var out []outbound
	for _, occ := range office.Advance(SimStep.Seconds(), now) {
		s.finish(occ, now)
		over, err := json.Marshal(Over{
			T:     TypeOver,
			Cause: occ.Cause,
			Pay:   rub(occ.State.Salary),
			Secs:  int(occ.Elapsed(now)),
		})
		if err != nil {
			continue
		}
		for _, c := range conns[occ.AccountID] {
			out = append(out, outbound{c, over})
		}
	}

	// Every second tick, so the wire runs at half the simulation rate. One
	// snapshot PER OCCUPANT, not one per office: a frame is addressed to its
	// reader and quantised from their point of view.
	if office.Tick()%SnapshotEvery == 0 {
		for account, cs := range conns {
			msg, ok := office.SnapshotFor(account)
			if !ok {
				continue
			}
			for _, c := range cs {
				out = append(out, outbound{c, msg})
			}
		}
	}

	if office.Empty() {
		s.office = nil
	}
	s.mu.Unlock()

	s.publish(ctx, out)
}

// publish writes every queued frame, giving up on a hub that has gone away.
func (s *Service) publish(ctx context.Context, out []outbound) {
	for _, o := range out {
		if err := s.transport.PublishTo(ctx, o.connID, o.msg); err != nil {
			if errors.Is(err, realtime.ErrHubClosed) || errors.Is(err, context.Canceled) {
				return
			}
		}
	}
}

// connsByAccount groups the room's members by the account behind them, so
// somebody with the game open on two devices sees the same shift on both.
func (s *Service) connsByAccount(ctx context.Context) map[string][]string {
	members, err := s.transport.Members(ctx, s.room)
	if err != nil {
		return nil
	}
	out := make(map[string][]string, len(members))
	for _, m := range members {
		out[m.AccountID] = append(out[m.AccountID], m.ConnID)
	}
	return out
}

// finish queues an ended shift for writing, if it lasted long enough to be one.
//
// A shift under MinShiftSeconds is dropped rather than written: an accidental
// tap that starts and ends in a second is not a result, and a leaderboard full
// of them is noise. The player is still told it ended — the over frame does not
// depend on the row.
func (s *Service) finish(occ *Occupant, now time.Time) {
	secs := occ.Elapsed(now)
	if secs < MinShiftSeconds {
		return
	}
	accountID, err := uuid.Parse(occ.AccountID)
	if err != nil {
		return
	}
	shiftID, err := uuid.Parse(occ.ShiftID)
	if err != nil {
		return
	}
	sh := Shift{
		ID:        shiftID,
		AccountID: accountID,
		Cause:     occ.Cause,
		Salary:    occ.State.Salary,
		Seconds:   secs,
	}
	select {
	case s.saves <- sh:
	default:
		slog.Warn("gamekaren: shift dropped, save queue full", "shift_id", sh.ID)
	}
}

// persistShifts is the one writer. It owns every INSERT this game makes, which
// is what lets the simulation loop above promise never to touch Postgres.
func (s *Service) persistShifts(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			// Drain what is already queued before going away — a deploy in the
			// middle of somebody's best shift should still record it. The
			// context is gone, so the writes get a fresh short-lived one.
			for {
				select {
				case sh := <-s.saves:
					s.writeShift(context.WithoutCancel(ctx), sh)
				default:
					return
				}
			}
		case sh := <-s.saves:
			s.writeShift(ctx, sh)
		}
	}
}

func (s *Service) writeShift(ctx context.Context, sh Shift) {
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.repo.InsertShift(wctx, s.q, sh); err != nil {
		slog.ErrorContext(ctx, "gamekaren: insert shift", "err", err, "shift_id", sh.ID)
	}
}

// StartShift puts an account to work and returns the new shift's id.
//
// It touches Postgres not at all: a shift is written when it ENDS, once. The
// office is built here if nobody was working, which is why an idle process
// simulates nothing.
//
// The context is taken and not used, and it is BLANK rather than named so that
// the godoc says so out loud. Every other verb on this service is a
// request-scoped call, and a handler that had to remember which one was
// different would eventually forget; the day this one reads the database, the
// parameter is already there and no caller changes.
func (s *Service) StartShift(ctx context.Context, accountID string) (string, error) {
	now := time.Now()
	// Read ONCE, here, and outside the lock: it is a database round trip, it is
	// constant for the life of the shift, and the alternative is a query on a
	// 20 Hz loop. A failure is not one — an office where somebody has no face is
	// the state this game shipped in, so it warns and carries on rather than
	// refusing to let a person work because their picture would not load.
	avatar := ""
	if s.profiles != nil {
		got, err := s.profiles.AvatarURL(ctx, accountID)
		if err != nil {
			slog.WarnContext(ctx, "gamekaren: avatar load failed", "err", err)
		} else {
			avatar = got
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.office == nil {
		s.office = NewOffice()
	}
	shiftID := uuid.New().String()
	if err := s.office.Join(accountID, shiftID, s.pseudonym(accountID), avatar, now); err != nil {
		// A refusal must not leave an empty office behind, or the bald man would
		// be left standing wherever the last shift ended.
		if s.office.Empty() {
			s.office = nil
		}
		return "", err
	}
	return shiftID, nil
}

// LeaveShift is the quit button: the shift ends with cause "left", is recorded
// if it lasted long enough, and every device the account has open is told.
//
// Telling them matters because the HTTP response is a bare 204 — the over screen
// needs the salary and the seconds, and the second tab needs to stop drawing an
// office nobody is in.
func (s *Service) LeaveShift(ctx context.Context, accountID string) error {
	now := time.Now()

	s.mu.Lock()
	if s.office == nil {
		s.mu.Unlock()
		return ErrNoShift
	}
	occ, ok := s.office.Leave(accountID)
	if !ok {
		s.mu.Unlock()
		return ErrNoShift
	}
	s.finish(occ, now)
	if s.office.Empty() {
		s.office = nil
	}
	s.mu.Unlock()

	over, err := json.Marshal(Over{
		T:     TypeOver,
		Cause: occ.Cause,
		Pay:   rub(occ.State.Salary),
		Secs:  int(occ.Elapsed(now)),
	})
	if err != nil {
		return nil
	}
	var out []outbound
	for _, c := range s.connsByAccount(ctx)[accountID] {
		out = append(out, outbound{c, over})
	}
	s.publish(ctx, out)
	return nil
}

// CurrentShift is which shift an account is working, if any. It is the reload
// path: a page that comes back finds its shift here rather than starting a
// second one.
func (s *Service) CurrentShift(accountID string) (string, bool) {
	s.mu.Lock()
	office := s.office
	s.mu.Unlock()
	if office == nil {
		return "", false
	}
	return office.ShiftOf(accountID)
}

// RecentShifts reads an account's own last shifts, newest first.
func (s *Service) RecentShifts(ctx context.Context, accountID string, limit int) ([]Shift, error) {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return nil, ErrNoShift
	}
	return s.repo.RecentShifts(ctx, s.q, id, clampLimit(limit))
}

// TopShifts is the leaderboard — the best shift per account.
func (s *Service) TopShifts(ctx context.Context, limit int) ([]Shift, error) {
	return s.repo.TopShifts(ctx, s.q, clampLimit(limit))
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return DefaultShiftLimit
	}
	if limit > MaxShiftLimit {
		return MaxShiftLimit
	}
	return limit
}

// PurgeAccount drops an account from the office WITHOUT recording anything.
//
// It is the admin «забыть» path, and three properties are load-bearing because
// of how it is called: it runs TWICE around the anonymising statement, so it
// must be idempotent; it is called for accounts that have never played, so an
// unknown one is a no-op rather than an error; and it runs on an HTTP handler's
// goroutine, so it must not block on the tick, on the hub or on Postgres.
//
// A shift belonging to somebody who is being erased is not a result, so unlike
// LeaveShift it queues nothing.
func (s *Service) PurgeAccount(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.office == nil {
		return
	}
	s.office.Leave(accountID)
	if s.office.Empty() {
		s.office = nil
	}
}

// HandleInbound implements realtime.Handler. It runs on the connection's own
// read pump, after the socket's rate check, so it must not block — which is why
// input is queued for the tick rather than simulated here.
func (s *Service) HandleInbound(ctx context.Context, m realtime.Member, room string, payload []byte) {
	if room != s.room {
		return
	}
	switch t, cmds := ParseInbound(payload); t {
	case TypeHello:
		s.hello(ctx, m)
	case TypeInput:
		now := time.Now()
		s.mu.Lock()
		office := s.office
		s.mu.Unlock()
		if office != nil {
			office.Enqueue(m.AccountID, cmds, now)
		}
	default:
		// Malformed, unknown or invalid: no reply and no log line. A log per bad
		// frame at the permitted ten a second is a flood lever handed to any
		// client.
	}
}

// hello attaches a connection to whatever shift the account already started over
// HTTP, and tells it which one that is.
//
// A hello with no shift gets silence. It is not an error state: it is a socket
// that opened before the shift did, or one that outlived it, and the client's
// own next move — pressing НАЧАТЬ СМЕНУ — is what fixes it.
func (s *Service) hello(ctx context.Context, m realtime.Member) {
	shiftID, ok := s.CurrentShift(m.AccountID)
	if !ok {
		return
	}
	msg, err := json.Marshal(Ready{T: TypeReady, ShiftID: shiftID})
	if err != nil {
		return
	}
	_ = s.transport.PublishTo(ctx, m.ConnID, msg)
}
