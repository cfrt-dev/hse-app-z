package api

import (
	"strings"
	"time"
	_ "time/tzdata" // lessons carry IANA zones; don't depend on the host's zoneinfo
)

// Moscow is the zone the HSE backend uses for naive timestamps.
var Moscow = mustLoad("Europe/Moscow", 3*3600)

func mustLoad(name string, fallbackOffset int) *time.Location {
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.FixedZone(name, fallbackOffset)
}

// LoadZone returns the named zone, falling back to Moscow.
func LoadZone(name string) *time.Location {
	if name == "" {
		return Moscow
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return Moscow
}

// ZoneForLng guesses a campus time zone from longitude. Perm (≈56°E) is UTC+5;
// Moscow, St. Petersburg and Nizhny Novgorod are UTC+3.
func ZoneForLng(lng float64) *time.Location {
	if lng >= 52 {
		return mustLoad("Asia/Yekaterinburg", 5*3600)
	}
	return Moscow
}

// ZoneForCampus maps a campus code (CAMPUS_MOS, CAMPUS_PERM, …) to its zone.
func ZoneForCampus(campus string) *time.Location {
	if strings.Contains(strings.ToUpper(campus), "PERM") {
		return mustLoad("Asia/Yekaterinburg", 5*3600)
	}
	return Moscow
}

// GeoPoint is a GeoJSON point: Coordinates are [longitude, latitude].
type GeoPoint struct {
	Type        string    `json:"type"`
	Coordinates []float64 `json:"coordinates"`
}

func (g *GeoPoint) Valid() bool { return g != nil && len(g.Coordinates) >= 2 }
func (g *GeoPoint) Lng() float64 {
	if !g.Valid() {
		return 0
	}
	return g.Coordinates[0]
}
func (g *GeoPoint) Lat() float64 {
	if !g.Valid() {
		return 0
	}
	return g.Coordinates[1]
}

// LatLng is the {lat, lng} object used by the food endpoints.
type LatLng struct {
	Lat Num `json:"lat"`
	Lng Num `json:"lng"`
}

func (l *LatLng) Valid() bool { return l != nil && l.Lat.OK && l.Lng.OK }

// ---------------------------------------------------------------- timetable

type StreamLink struct {
	Link        string `json:"link"`
	Description string `json:"description"`
}

// Lesson is one timetable entry from GET /v3/ruz/lessons.
type Lesson struct {
	ID                FlexString   `json:"id"`
	Discipline        string       `json:"discipline"`
	DisciplineID      FlexString   `json:"discipline_id"`
	DisciplineLink    string       `json:"discipline_link"`
	Type              string       `json:"type"`
	KindOfWork        string       `json:"kindOfWork"`
	Stream            string       `json:"stream"`
	GroupID           FlexString   `json:"group_id"`
	Auditorium        string       `json:"auditorium"`
	AuditoriumID      FlexString   `json:"auditorium_id"`
	Building          string       `json:"building"`
	BuildingID        FlexString   `json:"building_id"`
	City              string       `json:"city"`
	TimeZone          string       `json:"time_zone"`
	DateStart         FlexTime     `json:"date_start"`
	DateEnd           FlexTime     `json:"date_end"`
	CreatedAt         FlexTime     `json:"created_at"`
	UpdatedAt         FlexTime     `json:"updated_at"`
	ImportanceLevel   Num          `json:"importance_level"`
	Location          *GeoPoint    `json:"location"`
	LessonNumberStart Num          `json:"lesson_number_start"`
	LessonNumberEnd   Num          `json:"lesson_number_end"`
	LecturerEmails    []string     `json:"lecturer_emails"`
	LecturerProfiles  []Person     `json:"lecturer_profiles"`
	Note              string       `json:"note"`
	StreamLinks       []StreamLink `json:"stream_links"`
	IsBan             bool         `json:"is_ban"`
	Hash              string       `json:"hash"`
}

// Zone is the lesson's own time zone (Moscow when missing/unknown).
func (l Lesson) Zone() *time.Location { return LoadZone(l.TimeZone) }

// Start/End are the lesson bounds in the lesson's own zone.
func (l Lesson) Start() time.Time { return l.DateStart.In(l.Zone()) }
func (l Lesson) End() time.Time   { return l.DateEnd.In(l.Zone()) }

// Kind is the human lesson type, preferring kindOfWork.
func (l Lesson) Kind() string {
	if strings.TrimSpace(l.KindOfWork) != "" {
		return l.KindOfWork
	}
	return l.Type
}

// IsOnline reports whether the lesson is held online.
func (l Lesson) IsOnline() bool {
	a := strings.ToLower(strings.TrimSpace(l.Auditorium))
	return a == "online" || a == "онлайн" || strings.HasPrefix(a, "online") || strings.HasPrefix(a, "онлайн")
}

// Links returns every distinct URL attached to the lesson (stream links
// first, then a URL-looking note).
func (l Lesson) Links() []StreamLink {
	var out []StreamLink
	seen := map[string]bool{}
	for _, s := range l.StreamLinks {
		u := strings.TrimSpace(s.Link)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, StreamLink{Link: u, Description: s.Description})
	}
	for _, f := range strings.Fields(l.Note) {
		if (strings.HasPrefix(f, "http://") || strings.HasPrefix(f, "https://")) && !seen[f] {
			seen[f] = true
			out = append(out, StreamLink{Link: f})
		}
	}
	return out
}

// StreamName extracts the readable part of the RUZ stream code
// ("26_27_М_ТВИМС_Г_1159405_5#Г#Теория вероятностей…" → "Теория вероятностей…").
func (l Lesson) StreamName() string {
	if i := strings.LastIndex(l.Stream, "#"); i >= 0 && i+1 < len(l.Stream) {
		return l.Stream[i+1:]
	}
	return l.Stream
}

// LessonQuery selects whose timetable to load. Exactly one of Email, Group or
// Auditorium should be set; an empty query returns the caller's own lessons.
// Start and End are inclusive calendar dates.
type LessonQuery struct {
	Email      string
	Group      string // RUZ group id, e.g. "ruz47990" or "47990"
	Auditorium string // RUZ auditorium id, e.g. "ruz1033" or "526"
	Start      time.Time
	End        time.Time
}

// ------------------------------------------------------------------ people

// Person is a search hit, a lecturer profile, or a full profile. Search can
// also return groups (Type "GROUP") and rooms (Type "AUDITORIUM"), which use
// the Label/Room fields instead of FullName.
type Person struct {
	ID          FlexString `json:"id"`
	Type        string     `json:"type"` // STUDENT, STAFF, GROUP, AUDITORIUM
	FullName    string     `json:"full_name"`
	Email       string     `json:"email"`
	Description string     `json:"description"`
	AvatarURL   string     `json:"avatar_url"`
	HasPhone    bool       `json:"has_phone"`
	BirthDate   string     `json:"birth_date"`

	// GROUP search hits.
	Label       string `json:"label"`
	Course      Num    `json:"course"`
	ProgramName string `json:"program_name"`
	Kind        string `json:"kind"`

	// AUDITORIUM search hits.
	Room               string    `json:"room"`
	AuditoriumType     string    `json:"auditorium_type"`
	AuditoriumLocation *GeoPoint `json:"auditorium_location"`

	// Full profile (GET /v3/dump/email/{email}).
	Names                   *PersonNames    `json:"names"`
	IsTimetableAvailable    *bool           `json:"is_timetable_available"`
	IsSubordinatesAvailable *bool           `json:"is_subordinates_available"`
	Campus                  string          `json:"campus"`
	Education               []Education     `json:"education"`
	StaffAddress            []StaffAddress  `json:"staff_address"`
	StaffPositions          []StaffPosition `json:"staff_positions"`
}

const (
	TypeStudent    = "STUDENT"
	TypeStaff      = "STAFF"
	TypeGroup      = "GROUP"
	TypeAuditorium = "AUDITORIUM"
)

// DisplayName is the best human label for any search hit.
func (p Person) DisplayName() string {
	for _, s := range []string{p.FullName, p.Label, p.Room, p.Email} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return string(p.ID)
}

func (p Person) IsGroup() bool      { return strings.EqualFold(p.Type, TypeGroup) }
func (p Person) IsAuditorium() bool { return strings.EqualFold(p.Type, TypeAuditorium) }
func (p Person) IsPerson() bool     { return !p.IsGroup() && !p.IsAuditorium() }

// HasTimetable is false only when the profile explicitly says so.
func (p Person) HasTimetable() bool {
	return p.IsTimetableAvailable == nil || *p.IsTimetableAvailable
}

// HasSubordinates is true only when the profile explicitly says so.
func (p Person) HasSubordinates() bool {
	return p.IsSubordinatesAvailable != nil && *p.IsSubordinatesAvailable
}

// BirthDay returns the birth date without a bogus "0000" year
// (staff profiles hide the year that way).
func (p Person) BirthDay() string {
	b := strings.TrimSpace(p.BirthDate)
	if strings.HasPrefix(b, "0000-") {
		if t, err := time.Parse("01-02", strings.TrimPrefix(b, "0000-")); err == nil {
			return t.Format("2 January")
		}
		return strings.TrimPrefix(b, "0000-")
	}
	if t, err := time.Parse("2006-01-02", b); err == nil {
		return t.Format("2 January 2006")
	}
	return b
}

type PersonNames struct {
	LastName   string `json:"last_name"`
	FirstName  string `json:"first_name"`
	MiddleName string `json:"middle_name"`
}

type Education struct {
	ID               FlexString `json:"id"`
	UniversityTitle  string     `json:"university_title"`
	StartYear        FlexString `json:"start_year"`
	DegreeLevel      string     `json:"degree_level"`
	ProgramID        FlexString `json:"program_id"`
	ProgramTitle     string     `json:"program_title"`
	FacultyTitle     string     `json:"faculty_title"`
	Campus           string     `json:"campus"`
	GroupID          FlexString `json:"group_id"`
	GroupTitle       string     `json:"group_title"`
	SmartPlanProgram FlexString `json:"smart_plan_program_id"`
	Degree           string     `json:"degree"`
}

type StaffAddress struct {
	Label             string     `json:"label"`
	RoomCode          FlexString `json:"room_code"`
	IsMain            bool       `json:"is_main"`
	PhoneInternalExt  FlexString `json:"phone_internal_ext"`
	PhoneInternalFull FlexString `json:"phone_internal_full"`
	Campus            string     `json:"campus"`
	// PresenceType/PresenceTime describe office hours, e.g.
	// "Consultation time" / "Еженедельно в четверг с 16-00 …" (free text,
	// may contain a meeting link glued to the surrounding words).
	PresenceType string `json:"presence_type"`
	PresenceTime string `json:"presence_time"`
}

type StaffPosition struct {
	UnitName     string     `json:"unit_name"`
	UnitID       FlexString `json:"unit_id"`
	IsMain       bool       `json:"is_main"`
	PositionName string     `json:"position_name"`
	Chief        *Person    `json:"chief"`
}

// PersonLink is GET /v3/dump/email/{email}/link: the public hse.ru page.
type PersonLink struct {
	URL string `json:"url"`
}

// ------------------------------------------------------------------ grades

// Program is an educational program selector. Ratings use numeric IDs and
// "title"; grades use zero-padded string IDs and "name".
type Program struct {
	ID          FlexString `json:"id"`
	Title       string     `json:"title"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
}

func (p Program) DisplayName() string {
	if p.Title != "" {
		return p.Title
	}
	if p.Name != "" {
		return p.Name
	}
	return string(p.ID)
}

type GradesResponse struct {
	Items                  []Grade    `json:"items"`
	AvailableAcademicYears []string   `json:"available_academic_years"`
	SelectedAcademicYear   string     `json:"selected_academic_year"`
	SelectedProgram        FlexString `json:"selected_program"`
	AvailablePrograms      []Program  `json:"available_programs"`
	CurrentAcademicYear    string     `json:"current_academic_year"`
}

type GradeValue struct {
	TenPoint  Num   `json:"ten_point_scale"`
	FivePoint Num   `json:"five_point_scale"`
	Pass      *bool `json:"pass"`
}

type Grade struct {
	ID            FlexString  `json:"id"`
	ProgramID     FlexString  `json:"program_id"`
	Discipline    string      `json:"discipline"`
	TypeRaw       string      `json:"type_raw"`
	RepassCount   Num         `json:"repass_count"`
	Grade         *GradeValue `json:"grade"`
	Date          FlexTime    `json:"date"`
	ModuleName    string      `json:"module_name"`
	ModuleNum     FlexString  `json:"module_num"`
	PeriodCredits Num         `json:"period_credits"`
	Credits       Num         `json:"credits"`
	EntireHours   Num         `json:"entire_hours"`
	AudHours      Num         `json:"aud_hours"`
	AcademicYear  string      `json:"academic_year"`
	Lecturer      string      `json:"lecturer"`
}

// HasGrade reports whether any mark (numeric or pass/fail) is present.
func (g Grade) HasGrade() bool {
	return g.Grade != nil && (g.Grade.TenPoint.OK || g.Grade.Pass != nil)
}

// GradeText is the short mark: "9", "pass", "fail" or "" when not graded yet.
// A numeric mark wins over the pass flag (the API sends pass=false next to
// numeric exam grades).
func (g Grade) GradeText() string {
	if g.Grade == nil {
		return ""
	}
	if g.Grade.TenPoint.OK {
		return g.Grade.TenPoint.String()
	}
	if g.Grade.Pass != nil {
		if *g.Grade.Pass {
			return "pass"
		}
		return "fail"
	}
	return ""
}

// ----------------------------------------------------------------- ratings

type RatingsResponse struct {
	Items                  []Rating   `json:"items"`
	SelectedAcademicYear   string     `json:"selected_academic_year"`
	SelectedType           string     `json:"selected_type"`
	SelectedProgram        FlexString `json:"selected_program"`
	SelectedModuleNumber   Num        `json:"selected_module_number"`
	AvailablePrograms      []Program  `json:"available_programs"`
	AvailableTypes         []string   `json:"available_types"`
	AvailableModuleNumbers []Num      `json:"available_module_numbers"`
	CurrentAcademicYear    string     `json:"current_academic_year"`
	AvailableAcademicYears []string   `json:"available_academic_years"`
}

type RatingDiscipline struct {
	Discipline    string `json:"discipline"`
	CurrentRating Num    `json:"current_rating"` // credit weight of the discipline
	Grade         Num    `json:"grade"`
}

// Rating is one published rating snapshot. Types: current, cumul, retake, minor.
type Rating struct {
	LearnPeriod        string             `json:"learn_period"`
	LearnYear          string             `json:"learn_year"`
	PublishedAt        FlexTime           `json:"published_at"`
	Title              string             `json:"title"`
	Type               string             `json:"type"`
	LearnProgram       string             `json:"learn_program"`
	Faculty            string             `json:"faculty"`
	Group              string             `json:"group"`
	Course             Num                `json:"course"`
	Credits            Num                `json:"credits"`
	CreditsPrecise     Num                `json:"credits_precise"`
	Degree             string             `json:"degree"`
	Campus             string             `json:"campus"`
	GradeMid           Num                `json:"grade_mid"`
	GradeMin           Num                `json:"grade_min"`
	GradeMinor         Num                `json:"grade_minor"`
	Percentil          Num                `json:"percentil"`
	ExamAll            Num                `json:"exam_all"`
	Exam3              Num                `json:"exam3"`
	Exam5              Num                `json:"exam5"`
	Exam7              Num                `json:"exam7"`
	Exam10             Num                `json:"exam10"`
	ExamB              Num                `json:"examb"`
	ExamE              Num                `json:"exame"`
	ExamF              Num                `json:"examf"`
	ExamG              Num                `json:"examg"`
	ExamN              Num                `json:"examn"`
	ExamP              Num                `json:"examp"`
	ExamZ              Num                `json:"examz"`
	ExamRetake         Num                `json:"examretake"`
	ExamRetakeGood     Num                `json:"examretakegood"`
	PlaceCurr          Num                `json:"place_curr"`
	PlaceTotal         Num                `json:"place_total"`
	PlaceGroupOpCurr   Num                `json:"place_group_op_curr"`
	PlaceGroupOpTotal  Num                `json:"place_group_op_total"`
	PlaceOpCurr        Num                `json:"place_op_curr"`
	PlaceOpTotal       Num                `json:"place_op_total"`
	PlaceOpCourseCurr  Num                `json:"place_op_course_curr"`
	PlaceOpCourseTotal Num                `json:"place_op_course_total"`
	GPA                Num                `json:"gpa"`
	RatingKoef         Num                `json:"rating_koef"`
	RatingNorm         Num                `json:"rating_norm"`
	RatingGroupNorm    Num                `json:"rating_group_norm"`
	RatingGroupKoef    Num                `json:"rating_group_koef"`
	Rating             Num                `json:"rating"`
	DisciplineList     []RatingDiscipline `json:"discipline_list"`
}

// RatingQuery filters GET /education/ratings. Zero values are omitted.
type RatingQuery struct {
	AcademicYear string
	ProgramID    string
	Type         string // current, cumul, retake, minor
	ModuleNumber int
}

// ---------------------------------------------------------- campus & food

type OpeningHours struct {
	DayOfWeek string `json:"day_of_week"` // monday … sunday
	IsOpen    bool   `json:"is_open"`
	StartTime string `json:"start_time"` // "09:00", may be empty
	EndTime   string `json:"end_time"`
}

type CafeNavigation struct {
	Room  FlexString `json:"room"`
	Floor FlexString `json:"floor"`
}

type Cafe struct {
	ID           FlexString      `json:"cafe_id"`
	Name         string          `json:"cafe_name"`
	OpeningHours []OpeningHours  `json:"opening_hours"`
	ClosedDates  []string        `json:"closed_dates"`
	Address      string          `json:"address"`
	Coordinates  *LatLng         `json:"coordinates"`
	HasMenu      bool            `json:"has_menu"`
	Photos       []string        `json:"photos"`
	Photo        string          `json:"photo"`
	Description  string          `json:"description"`
	CurrentLoad  string          `json:"current_load"`
	Navigation   *CafeNavigation `json:"navigation"`
	Banner       string          `json:"banner"`
}

// CafeGroup is one element of GET /food/cafes: the cafes of one building.
type CafeGroup struct {
	CampusID    FlexString `json:"campus_id"` // actually a building id
	CampusName  string     `json:"campus_name"`
	Coordinates *LatLng    `json:"coordinates"`
	Cafes       []Cafe     `json:"cafes"`
}

type MenuItem struct {
	ItemName    string     `json:"item_name"`
	ItemNameOpt string     `json:"item_name_opt"` // name in the other language
	ItemID      FlexString `json:"item_id"`
	Price       Num        `json:"price"`
	Weight      string     `json:"weight"`
	Calories    string     `json:"calories"`
	Composition string     `json:"composition"`
	Chips       []string   `json:"chips"` // vegetarian, dietary, …
	Section     string     `json:"section"`
}

type MenuSection struct {
	SectionName string     `json:"section_name"`
	Price       Num        `json:"price"` // set price for complex lunches
	Section     string     `json:"section"`
	Items       []MenuItem `json:"items"`
}

// Menu is GET /food/cafes/{id}/menu[?day=monday].
type Menu struct {
	CafeID        FlexString    `json:"cafe_id"`
	CurrentDay    string        `json:"current_day"`
	AvailableDays []string      `json:"available_days"`
	Sections      []MenuSection `json:"sections"`
}

type LibrarySchedule struct {
	OpeningHours []OpeningHours `json:"opening_hours"`
	SanitaryDay  string         `json:"sanitary_day"`
}

type LibraryRule struct {
	Name     string           `json:"name"`
	Schedule *LibrarySchedule `json:"schedule"`
}

type LibraryOffice struct {
	Name         string        `json:"name"`
	PhoneNumbers []string      `json:"phone_numbers"`
	AddressHint  string        `json:"address_hint"`
	Rules        []LibraryRule `json:"rules"`
}

type Library struct {
	ID          FlexString      `json:"_id"`
	Name        string          `json:"name"`
	BuildingID  FlexString      `json:"building_id"`
	AddressHint string          `json:"address_hint"`
	Index       Num             `json:"index"`
	Offices     []LibraryOffice `json:"offices"`
}

type Building struct {
	ID          FlexString `json:"id"`
	Name        string     `json:"name"`
	Address     string     `json:"address"`
	Campus      string     `json:"campus"`
	Location    *GeoPoint  `json:"location"`
	Cafes       []Cafe     `json:"cafes"`
	LibrariesV3 []Library  `json:"libraries_v3"`
}

// CampusGroup is one element of POST /v3/dump/buildings/groups.
type CampusGroup struct {
	Campus    string     `json:"campus"`
	Near      bool       `json:"near"`
	Location  *GeoPoint  `json:"location"`
	Buildings []Building `json:"buildings"`
}

// CampusName turns CAMPUS_MOS etc. into a readable city name.
func CampusName(code string) string {
	switch strings.ToUpper(code) {
	case "CAMPUS_MOS":
		return "Moscow"
	case "CAMPUS_SPB":
		return "St. Petersburg"
	case "CAMPUS_NNOV":
		return "Nizhny Novgorod"
	case "CAMPUS_PERM":
		return "Perm"
	case "":
		return "Other"
	}
	return strings.TrimPrefix(code, "CAMPUS_")
}

// --------------------------------------------------------------- services

// Service is an entry of GET /tasks/services. Entries with a URL are links;
// entries with only a Category are folders that open another listing via
// GET /tasks/services?category=<Category>.
type Service struct {
	ID          FlexString `json:"id"`
	Category    string     `json:"category"`
	URL         string     `json:"url"`
	Icon        string     `json:"icon"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
}

func (s Service) IsFolder() bool {
	return strings.TrimSpace(s.URL) == "" && strings.TrimSpace(s.Category) != ""
}

// TasksPage is GET /tasks: the user's SmartPoint requests. Item shape is not
// documented, so items are kept as generic JSON objects.
type TasksPage struct {
	Cursor     FlexString       `json:"cursor"`
	NextCursor FlexString       `json:"next_cursor"`
	Items      []map[string]any `json:"items"`
}

// ------------------------------------------------------------ news/banners

type StoryAction struct {
	Title string `json:"title"`
	Link  string `json:"link"`
}

type StoryPage struct {
	ID            FlexString   `json:"id"`
	Duration      Num          `json:"duration"`
	DatePublished FlexTime     `json:"date_published"`
	Action        *StoryAction `json:"action"`
	Image         string       `json:"image"`
	Title         string       `json:"title"`
	Text          string       `json:"text"`
}

type StoryPublisher struct {
	ID    FlexString `json:"id"`
	Image string     `json:"image"`
	Name  string     `json:"name"`
}

// Story is an element of GET /v2/stories.
type Story struct {
	ID           FlexString     `json:"id"`
	Title        string         `json:"title"`
	PreviewImage string         `json:"preview_image"`
	Publisher    StoryPublisher `json:"publisher"`
	Pages        []StoryPage    `json:"pages"`
}

// Published is the earliest page publication date (zero if unknown).
func (s Story) Published() time.Time {
	var t time.Time
	for _, p := range s.Pages {
		if !p.DatePublished.IsZero() && (t.IsZero() || p.DatePublished.Before(t)) {
			t = p.DatePublished.Time
		}
	}
	return t
}

// Banner is an element of GET /banners[?section=…].
type Banner struct {
	ID            FlexString `json:"id"`
	IsDismissible bool       `json:"is_dismissible"`
	IsEnabled     *bool      `json:"is_enabled"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Section       string     `json:"section"`
	URL           string     `json:"url"`
	Link          string     `json:"link"`
}

func (b Banner) Enabled() bool { return b.IsEnabled == nil || *b.IsEnabled }

// Href is the banner's link, if any.
func (b Banner) Href() string {
	if b.URL != "" {
		return b.URL
	}
	return b.Link
}
