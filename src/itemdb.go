package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ItemRec mirrors the compact oc-itemdb record format (see itemdb/build-itemdb.mjs).
type ItemRec struct {
	Name    string     `json:"n"`
	Icon    int        `json:"ic"`
	Slots   int        `json:"sl"`
	AC      int        `json:"ac"`
	HP      int        `json:"hp"`
	Mana    int        `json:"mn"`
	Endur   int        `json:"en"`
	Stats   []int      `json:"st"` // STR STA AGI DEX WIS INT CHA
	Resists []int      `json:"rs"` // MR FR CR DR PR
	Weight  int        `json:"wt"` // tenths
	Size    int        `json:"sz"`
	Magic   int        `json:"mg"`
	Lore    int        `json:"lo"`
	NoTrade int        `json:"nd"`
	NoRent  int        `json:"nr"`
	Classes int        `json:"cl"`
	Races   int        `json:"ra"`
	ReqLvl  int        `json:"rq"`
	RecLvl  int        `json:"rc"`
	Damage  int        `json:"dmg"`
	Delay   int        `json:"dly"`
	Attack  int        `json:"atk"`
	Haste   int        `json:"hst"`
	BagSlot int        `json:"bs"`
	Effects [][]string `json:"eff"`
}

// ItemDB serves item stats and icons from the local oc-itemdb copy.
type ItemDB struct {
	items map[int64]*ItemRec
	icons *zip.ReadCloser
	byIco map[string]*zip.File
}

// LoadItemDB reads oc-itemdb-all.json and opens icons.zip from dir.
// The icons zip stays open for the process lifetime.
func LoadItemDB(dir string) (*ItemDB, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "oc-itemdb-all.json"))
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Version int                 `json:"version"`
		Items   map[string]*ItemRec `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("oc-itemdb-all.json: %w", err)
	}
	// v1 data had classes misfiled into the races column (generator off-by-one);
	// this bot only accepts the corrected v2 format. Migrate v1 files with
	// itemdb/migrate-itemdb-v2.mjs.
	if wrapper.Version < 2 {
		return nil, fmt.Errorf("oc-itemdb-all.json is version %d; run migrate-itemdb-v2.mjs to upgrade it to v2", wrapper.Version)
	}
	db := &ItemDB{items: make(map[int64]*ItemRec, len(wrapper.Items)), byIco: map[string]*zip.File{}}
	for k, v := range wrapper.Items {
		var id int64
		if _, err := fmt.Sscanf(k, "%d", &id); err == nil {
			db.items[id] = v
		}
	}
	// supplement.json: items backfilled from Magelo for traded items the base db
	// never had (out-of-era drops, items absent from the P99 lists). Fill-only -
	// the base db always wins - so it never overrides the era-accurate records.
	if raw, err := os.ReadFile(filepath.Join(dir, "supplement.json")); err == nil {
		var sup struct {
			Items map[string]*ItemRec `json:"items"`
		}
		if err := json.Unmarshal(raw, &sup); err != nil {
			return nil, fmt.Errorf("supplement.json: %w", err)
		}
		added := 0
		for k, v := range sup.Items {
			var id int64
			if _, err := fmt.Sscanf(k, "%d", &id); err == nil {
				if _, exists := db.items[id]; !exists {
					db.items[id] = v
					added++
				}
			}
		}
		if added > 0 {
			fmt.Printf("itemdb: %d supplement record(s) added\n", added)
		}
	}
	// overrides.json: hand-corrected records (same format) merged over the
	// generated data - fixes bad upstream rows without touching built files.
	if raw, err := os.ReadFile(filepath.Join(dir, "overrides.json")); err == nil {
		var ov map[string]*ItemRec
		if err := json.Unmarshal(raw, &ov); err != nil {
			return nil, fmt.Errorf("overrides.json: %w", err)
		}
		for k, v := range ov {
			var id int64
			if _, err := fmt.Sscanf(k, "%d", &id); err == nil {
				db.items[id] = v
			}
		}
		fmt.Printf("itemdb: %d override record(s) applied\n", len(ov))
	}
	if z, err := zip.OpenReader(filepath.Join(dir, "icons.zip")); err == nil {
		db.icons = z
		for _, f := range z.File {
			db.byIco[f.Name] = f
		}
	}
	return db, nil
}

func (db *ItemDB) Get(id int64) *ItemRec {
	if db == nil {
		return nil
	}
	return db.items[id]
}

func (db *ItemDB) Count() int {
	if db == nil {
		return 0
	}
	return len(db.items)
}

// IconPNG returns the raw PNG for an icon id, or nil if unavailable.
func (db *ItemDB) IconPNG(icon int) []byte {
	if db == nil || db.icons == nil || icon <= 0 {
		return nil
	}
	f, ok := db.byIco[fmt.Sprintf("%d.png", icon)]
	if !ok {
		return nil
	}
	r, err := f.Open()
	if err != nil {
		return nil
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return nil
	}
	return b
}

// ---- bitmask decoding (standard EQ conventions from the sodeq dump) ----

var classNames = []string{"WAR", "CLR", "PAL", "RNG", "SHD", "DRU", "MNK", "BRD", "ROG", "SHM", "NEC", "WIZ", "MAG", "ENC", "BST", "BER"}
var raceNames = []string{"HUM", "BAR", "ERU", "ELF", "HIE", "DEF", "HEF", "DWF", "TRL", "OGR", "HFL", "GNM", "IKS", "VAH", "FRG", "DRK"}
var slotNames = []struct {
	bit  int
	name string
}{
	{1, "CHARM"}, {2, "EAR"}, {4, "HEAD"}, {8, "FACE"}, {16, "EAR"}, {32, "NECK"},
	{64, "SHOULDERS"}, {128, "ARMS"}, {256, "BACK"}, {512, "WRIST"}, {1024, "WRIST"},
	{2048, "RANGE"}, {4096, "HANDS"}, {8192, "PRIMARY"}, {16384, "SECONDARY"},
	{32768, "FINGER"}, {65536, "FINGER"}, {131072, "CHEST"}, {262144, "LEGS"},
	{524288, "FEET"}, {1048576, "WAIST"}, {2097152, "AMMO"},
}
var sizeNames = []string{"TINY", "SMALL", "MEDIUM", "LARGE", "GIANT"}

func decodeMask(mask int, names []string, all string) string {
	if mask <= 0 {
		return ""
	}
	if mask >= (1<<len(names))-1 {
		return all
	}
	var out []string
	for i, n := range names {
		if mask&(1<<i) != 0 {
			out = append(out, n)
		}
	}
	return strings.Join(out, " ")
}

func decodeSlots(mask int) string {
	var out []string
	seen := map[string]bool{}
	for _, s := range slotNames {
		if mask&s.bit != 0 && !seen[s.name] {
			out = append(out, s.name)
			seen[s.name] = true
		}
	}
	return strings.Join(out, " ")
}

var statLabels = []string{"STR", "STA", "AGI", "DEX", "WIS", "INT", "CHA"}
var resistLabels = []string{"SV MAGIC", "SV FIRE", "SV COLD", "SV DISEASE", "SV POISON"}

func signedList(vals []int, labels []string) string {
	var out []string
	for i, v := range vals {
		if v != 0 && i < len(labels) {
			out = append(out, fmt.Sprintf("%s %+d", labels[i], v))
		}
	}
	return strings.Join(out, "  ")
}

// StatsBlock renders the classic in-game tooltip as compact embed markdown.
func (r *ItemRec) StatsBlock() string {
	var lines []string
	var tags []string
	if r.Magic != 0 {
		tags = append(tags, "MAGIC")
	}
	if r.Lore != 0 {
		tags = append(tags, "LORE")
	}
	if r.NoTrade != 0 {
		tags = append(tags, "NO TRADE")
	}
	if r.NoRent != 0 {
		tags = append(tags, "NO RENT")
	}
	if len(tags) > 0 {
		lines = append(lines, "**"+strings.Join(tags, " · ")+"**")
	}
	if s := decodeSlots(r.Slots); s != "" {
		lines = append(lines, "Slot: "+s)
	}
	if r.Damage > 0 && r.Delay > 0 {
		lines = append(lines, fmt.Sprintf("DMG %d  DLY %d  (ratio %.2f)", r.Damage, r.Delay, float64(r.Damage)/float64(r.Delay)))
	}
	var core []string
	if r.AC != 0 {
		core = append(core, fmt.Sprintf("AC %d", r.AC))
	}
	if r.HP != 0 {
		core = append(core, fmt.Sprintf("HP %+d", r.HP))
	}
	if r.Mana != 0 {
		core = append(core, fmt.Sprintf("MANA %+d", r.Mana))
	}
	if r.Endur != 0 {
		core = append(core, fmt.Sprintf("END %+d", r.Endur))
	}
	if r.Attack != 0 {
		core = append(core, fmt.Sprintf("ATK %+d", r.Attack))
	}
	if r.Haste != 0 {
		core = append(core, fmt.Sprintf("HASTE %d%%", r.Haste))
	}
	if len(core) > 0 {
		lines = append(lines, strings.Join(core, "  "))
	}
	if s := signedList(r.Stats, statLabels); s != "" {
		lines = append(lines, s)
	}
	if s := signedList(r.Resists, resistLabels); s != "" {
		lines = append(lines, s)
	}
	for _, e := range r.Effects {
		if len(e) == 2 {
			if e[0] == "" {
				lines = append(lines, "Effect: "+e[1])
			} else {
				lines = append(lines, fmt.Sprintf("Effect: %s (%s)", e[1], e[0]))
			}
		}
	}
	var meta []string
	if c := decodeMask(r.Classes, classNames, "ALL"); c != "" {
		meta = append(meta, "Class: "+c)
	}
	if ra := decodeMask(r.Races, raceNames, "ALL"); ra != "" {
		meta = append(meta, "Race: "+ra)
	}
	if len(meta) > 0 {
		lines = append(lines, strings.Join(meta, " · "))
	}
	var tail []string
	if r.Weight > 0 {
		tail = append(tail, fmt.Sprintf("WT %.1f", float64(r.Weight)/10))
	}
	if r.Size >= 0 && r.Size < len(sizeNames) {
		tail = append(tail, "Size "+sizeNames[r.Size])
	}
	if r.ReqLvl > 0 {
		tail = append(tail, fmt.Sprintf("Req lvl %d", r.ReqLvl))
	}
	if r.BagSlot > 0 {
		tail = append(tail, fmt.Sprintf("%d-slot container", r.BagSlot))
	}
	if len(tail) > 0 {
		lines = append(lines, strings.Join(tail, " · "))
	}
	return strings.Join(lines, "\n")
}
