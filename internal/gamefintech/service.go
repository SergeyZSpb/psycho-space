package gamefintech

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
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

// Working is a shift somebody is in: which one, who they are, when it started,
// and the floor it is being played on.
//
// A STRUCT RATHER THAN FOUR RETURN VALUES, because the office id arrived fourth
// and a four-value signature at two call sites is where a caller starts passing
// them in the wrong order. StartTick is zero from StartShift — the caller already
// knows the shift has just begun — and carries the office tick on a resume.
type Working struct {
	ShiftID   string
	Persona   int
	StartTick uint64
	// OfficeID is the content hash of the floor this shift was joined on. A
	// client compares it against the catalogue it cached at mount and refetches
	// when they differ, which is the whole of the staleness contract.
	OfficeID string
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
	// plan is the floor in force: the layout the next office will be opened on,
	// and the one the catalogue serves.
	//
	// HELD BY THE SERVICE AND NOT BY THE OFFICE, because it outlives every office
	// — an empty process still has to be able to tell a splash screen what the
	// room looks like. An office is opened on whatever plan this holds and keeps
	// its own pointer to it for its whole life, so a floor that changes never
	// changes underneath a shift in progress: it changes what the NEXT office is
	// built on.
	plan *Plan
	// source and installedAt are where the floor came from and when it arrived.
	// Carried for the admin section, which says both out loud, and for nothing
	// else: neither is derivable from the geometry.
	source      string
	installedAt time.Time
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

// NewService builds the game on a floor.
//
// The layout is a parameter because reading it is a database round trip and this
// constructor takes no context: the composition root reads it once, at boot, with
// EnsureLayout, and hands it over. That also makes the failure mode obvious —
// there is no state in which this service is running without a floor.
func NewService(t Transport, room string, q db.DBTX, repo Repository, profiles Profiles, floor StoredLayout) *Service {
	return &Service{
		transport:    t,
		room:         room,
		q:            q,
		repo:         repo,
		profiles:     profiles,
		plan:         NewPlan(floor.Layout),
		source:       floor.Source,
		installedAt:  floor.InstalledAt,
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

// Floor is the office in force, as the admin section reads it: the geometry,
// where it came from, when it arrived, and how many people are standing on it.
//
// A SEPARATE TYPE FROM StoredLayout because the occupant count is not a property
// of the stored row — it is a fact about right now, and it is the one thing on
// the admin page that answers «what happens to somebody if I press this».
type Floor struct {
	Layout      Layout
	Source      string
	InstalledAt time.Time
	Occupants   int
}

// Floor reports the office in force. Read under the lock, in one pass, so the
// count and the geometry describe the same instant.
func (s *Service) Floor() Floor {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := Floor{Layout: s.plan.Layout(), Source: s.source, InstalledAt: s.installedAt}
	if s.office != nil {
		f.Occupants = s.office.Occupants()
	}
	return f
}

// LayoutInvalidError is a floor that may not be installed, and which of its rules
// it broke. The handler turns the issues into codes the editor highlights; it
// never turns this into a sentence for a client.
type LayoutInvalidError struct{ Issues []LayoutIssue }

func (e LayoutInvalidError) Error() string { return "gamefintech: the layout is not playable" }

// Renovate rebuilds the office: a new floor, drawn at random, and everybody
// currently working thrown out with the «РЕМОНТ» ending. It answers how many
// shifts it ended.
//
// The seed comes from crypto/rand rather than from a clock or a counter, for the
// reason «ВАНЯДУМ» gives about its буildings: a clock seed makes two offices
// generated in the same second identical, and a counter makes them ordered.
func (s *Service) Renovate(ctx context.Context) (int, error) {
	l, err := Generate(cryptoSeed())
	if err != nil {
		return 0, err
	}
	return s.Install(ctx, l, SourceGenerated)
}

// Install puts a floor in force: it validates, stores it, throws everybody out,
// and leaves the next shift to open an office on it. It answers how many shifts
// it ended.
//
// THE ORDER IS THE DESIGN. Validation and the INSERT both happen with no lock
// held, because the simulation takes that mutex twenty times a second and this
// package's whole persistence design — the buffered saves channel, the single
// writer — exists to keep Postgres off it. Only the swap itself is under the
// lock, and the frames it produces are published after it is released, exactly as
// the tick does.
//
// A REFUSED FLOOR CHANGES NOTHING. Neither an invalid layout nor a failed write
// touches the office: the people working carry on, on the floor they are standing
// on, and the caller is told why.
//
// It DROPS the office rather than retrofitting the new plan into it. See
// Office.EvictAll: the лысый, Claude, the colleagues and the props were placed
// against the floor that is going away.
func (s *Service) Install(ctx context.Context, l Layout, source string) (int, error) {
	if issues := ValidateLayout(l); len(issues) > 0 {
		return 0, LayoutInvalidError{Issues: issues}
	}
	l = l.WithID()
	plan := NewPlan(l)
	now := time.Now()

	// Both before the lock: one is a database round trip and the other is a hub
	// call, and neither may happen with the simulation's mutex held.
	if err := s.repo.InsertLayout(ctx, s.q, l, source); err != nil {
		return 0, err
	}
	conns := s.connsByAccount(ctx)

	s.mu.Lock()
	s.plan, s.source, s.installedAt = plan, source, now
	var (
		out   []outbound
		ended int
	)
	if s.office != nil {
		for _, occ := range s.office.EvictAll() {
			ended++
			s.finish(occ, now)
			over, err := overFrame(occ, now)
			if err != nil {
				continue
			}
			for _, c := range conns[occ.AccountID] {
				out = append(out, outbound{c, over})
			}
		}
		s.office = nil
	}
	s.mu.Unlock()

	// A SILENT MASS EVICTION IS THE FAILURE MODE WORTH A LOG LINE. Members can
	// answer an error, which yields a nil map — and then every one of those people
	// is left watching an office that has stopped moving, with no frame to tell
	// them why. It is unlikely and it is invisible from the outside, so it says so
	// here rather than nowhere.
	if ended > 0 && len(out) == 0 {
		slog.WarnContext(ctx, "gamefintech: office renovated with nobody told", "ended", ended)
	}
	slog.InfoContext(ctx, "gamefintech: office renovated",
		"layout_id", l.ID, "source", source, "ended", ended)
	s.publish(ctx, out)
	return ended, nil
}

// cryptoSeed draws the seed a generated floor comes from.
//
// Masked to 63 bits so the value is non-negative and reads sanely in a log line;
// a negative seed would work perfectly well, and looking like a bug is the only
// thing wrong with it.
func cryptoSeed() int64 {
	var b [8]byte
	// crypto/rand.Read is documented never to fail and panics internally if the
	// system source is broken, so there is nothing here to handle.
	_, _ = rand.Read(b[:])
	n := binary.BigEndian.Uint64(b[:]) >> 1
	return int64(n) //nolint:gosec // masked to 63 bits above, so it cannot be negative.
}

// Config is the served catalogue, including the floor in force.
//
// Under the lock, because the floor can be replaced while a request is being
// served — and the layout it hands out is a copy (BuildConfig), so the encoder
// never reads a slice the simulation is reading.
func (s *Service) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return BuildConfig(s.plan.Layout())
}

// EnsureLayout is the boot path: the floor in force, or the starting one written
// and returned when there is nothing stored yet.
//
// A MISSING FLOOR IS A STATE AND A BROKEN ONE IS NOT. An empty table is what a
// fresh database looks like, so it installs the starting floor and says so. A row
// that does not validate is a different matter — an older or hand-mangled
// geometry the simulation's single-pass resolver has no answer for — so it is
// refused in favour of the starting floor rather than served, and the log line is
// what makes that visible.
func EnsureLayout(ctx context.Context, q db.DBTX, repo Repository) (StoredLayout, error) {
	l, err := repo.CurrentLayout(ctx, q)
	switch {
	case err == nil:
		if issues := ValidateLayout(l.Layout); len(issues) > 0 {
			slog.ErrorContext(ctx, "gamefintech: stored layout is not playable, using the starting one",
				"problem", issues[0].Problem, "index", issues[0].Index)
			break
		}
		return l, nil
	case errors.Is(err, ErrNoLayout):
	default:
		return StoredLayout{}, err
	}

	l = StoredLayout{Layout: StartingLayout.WithID(), Source: SourceStarting, InstalledAt: time.Now()}
	if err := repo.InsertLayout(ctx, q, l.Layout, SourceStarting); err != nil {
		// Standing up on the in-memory floor beats refusing to boot: this table is
		// one game's furniture, and the landing page, the wishlist and three other
		// games have nothing to do with it.
		slog.ErrorContext(ctx, "gamefintech: could not store the starting layout", "err", err)
	}
	return l, nil
}

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
		over, err := overFrame(occ, now)
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

// overFrame is the «your shift ended» payload, marshalled.
//
// EXTRACTED AT ITS THIRD CALL SITE and not before: the tick's end loop and the
// quit button had built it inline, and a third copy in the install path is where
// three literals start disagreeing about which fields an ending carries.
func overFrame(occ *Occupant, now time.Time) ([]byte, error) {
	return json.Marshal(Over{
		T:     TypeOver,
		Cause: occ.Cause,
		Pay:   rub(occ.State.Salary),
		Secs:  int(occ.Elapsed(now)),
	})
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
		slog.Warn("gamefintech: shift dropped, save queue full", "shift_id", sh.ID)
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
		slog.ErrorContext(ctx, "gamefintech: insert shift", "err", err, "shift_id", sh.ID)
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
func (s *Service) StartShift(ctx context.Context, accountID string) (Working, error) {
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
			slog.WarnContext(ctx, "gamefintech: avatar load failed", "err", err)
		} else {
			avatar = got
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.office == nil {
		s.office = NewOffice(s.plan)
	}
	shiftID := uuid.New().String()
	persona := drawPersona()
	if err := s.office.Join(accountID, shiftID, s.pseudonym(accountID), avatar, persona, now); err != nil {
		// A refusal must not leave an empty office behind, or the bald man would
		// be left standing wherever the last shift ended.
		if s.office.Empty() {
			s.office = nil
		}
		return Working{}, err
	}
	// THE OFFICE ID IS READ HERE, inside the same critical section as the join,
	// and never from a second Config() call afterwards. Between the two the floor
	// could be replaced — and telling a client the id of a floor its shift was not
	// joined on is exactly the stranding the id exists to prevent.
	return Working{
		ShiftID:  shiftID,
		Persona:  persona,
		OfficeID: s.office.plan.Layout().ID,
	}, nil
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

	over, err := overFrame(occ, now)
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

// CurrentShift is which shift an account is working, if any — its id, the
// employee they are, and the office tick they clocked in on. It is the reload
// path: a page that comes back finds its shift here rather than starting a
// second one.
func (s *Service) CurrentShift(accountID string) (Working, bool) {
	s.mu.Lock()
	office := s.office
	s.mu.Unlock()
	if office == nil {
		return Working{}, false
	}
	id, persona, k0, ok := office.ShiftOf(accountID)
	if !ok {
		return Working{}, false
	}
	return Working{
		ShiftID:   id,
		Persona:   persona,
		StartTick: k0,
		OfficeID:  office.plan.Layout().ID,
	}, true
}

// RecentShifts reads an account's own last shifts, newest first.
func (s *Service) RecentShifts(ctx context.Context, accountID string, limit int) ([]Shift, error) {
	id, err := uuid.Parse(accountID)
	if err != nil {
		return nil, ErrNoShift
	}
	return s.repo.RecentShifts(ctx, s.q, id, clampLimit(limit))
}

// TopShifts is the leaderboard by money — the best shift per account, this week.
func (s *Service) TopShifts(ctx context.Context, limit int) ([]Shift, error) {
	return s.repo.TopShifts(ctx, s.q, boardSince(), clampLimit(limit))
}

// TopShiftsBySeconds is the leaderboard by how long the shift lasted — the
// longest shift per account, which is the game's other scored dimension.
func (s *Service) TopShiftsBySeconds(ctx context.Context, limit int) ([]Shift, error) {
	return s.repo.TopShiftsBySeconds(ctx, s.q, boardSince(), clampLimit(limit))
}

// boardSince is the oldest a shift may be and still rank: BoardWindow ago.
//
// COMPUTED PER READ, from the wall clock, which is what makes the board age by
// itself — a record written last Tuesday drops off next Tuesday with nothing
// running and nothing scheduled. There is no clock to inject and none is added:
// what a test needs to control is the cutoff the repository receives, and that is
// already an argument.
func boardSince() time.Time { return time.Now().Add(-BoardWindow) }

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
	switch t, in := ParseInbound(payload); t {
	case TypeHello:
		s.hello(ctx, m)
	case TypeDo:
		verb, target := ParseVerb(payload)
		s.mu.Lock()
		office := s.office
		s.mu.Unlock()
		if office == nil {
			return
		}
		// The outcome is not replied to in either case: it arrives as the next
		// snapshot, which is what stops a client believing a verb the office
		// refused. An unknown verb is silence, like every other unreadable frame.
		switch verb {
		case VerbRedirect:
			office.Redirect(m.AccountID, target)
		case VerbRouter:
			// No target: there is one Claude and the effect is the whole office's.
			office.RouterDown(m.AccountID)
		}
	case TypeInput:
		now := time.Now()
		s.mu.Lock()
		office := s.office
		s.mu.Unlock()
		if office != nil {
			office.Enqueue(m.AccountID, in, now)
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
	w, ok := s.CurrentShift(m.AccountID)
	if !ok {
		return
	}
	// The office id is deliberately NOT on this frame. It rides the HTTP shift
	// response instead, which lands before the client builds anything — where a
	// ready frame arrives after, racing the first snapshot it would be reacting to.
	msg, err := json.Marshal(Ready{T: TypeReady, ShiftID: w.ShiftID, Persona: w.Persona, K0: w.StartTick})
	if err != nil {
		return
	}
	_ = s.transport.PublishTo(ctx, m.ConnID, msg)
}
