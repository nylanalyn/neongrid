package game

import "strings"

// Message templates. Placeholders in braces are filled by render; every
// template in a pool must use only the placeholders its caller supplies.

// iceNames are the named ICE programs runners run into.
var iceNames = []string{
	"KRAKEN v3", "Hellhound-9", "BLACK ORCHID", "Tessellate", "Wendigo", "Glasshouse",
	"Saint Elmo's Fire", "Mirrorworm", "Cold Iron", "LAZARUS", "Nightjar", "Tollkeeper",
}

// districtPlaces gives each district a few specific spots for flavor text.
var districtPlaces = map[string][]string{
	DistrictNeonMarket:        {"under the noodle-stall holo-signs", "behind a pachinko parlor", "in a rented kiosk full of burner decks"},
	DistrictFloodline:         {"in a half-drowned server basement", "on a rooftop above the tide", "inside a rusted pump station"},
	DistrictCorporateArcology: {"forty floors up a Helix tower", "in a borrowed executive washroom", "on a maintenance ledge outside the arcology"},
	DistrictOldTransit:        {"in a dead maglev car", "under the Old Transit switching yard", "on a platform nobody has used since the crash"},
	DistrictGhostQuarter:      {"in a squat full of dead terminals", "between two burned-out tenements", "in a basement with no address"},
}

var iceWinTemplates = []string{
	"{runner} slipped past {ice} {place} and walked off with a data shard.",
	"{runner} cracked {ice} wide open {place}. Shard secured.",
	"{ice} never saw {runner} coming. Clean extraction {place}.",
	"{runner} danced through {ice}'s kill-loop {place} and pocketed the payload.",
	"{runner} fed {ice} a ghost signature {place} and lifted a shard while it chased it.",
}

var iceUniqueTemplates = []string{
	"{runner} gutted {ice} {place} and found UNIQUE {item} in the wreckage.",
	"Behind {ice}, {runner} found something nobody was supposed to find: UNIQUE {item}.",
	"{runner} broke {ice} {place}. The vault behind it held UNIQUE {item}.",
}

var iceLossTemplates = []string{
	"{ice} caught {runner} {place}. They burned time shaking the trace.",
	"{runner} tripped {ice} and had to jack out hard {place}.",
	"{ice} bit {runner} {place}. Lost time, lost nerve, kept the deck.",
	"{runner} ran straight into {ice} {place} and barely got out.",
	"{ice} backtraced {runner} {place}. They spent the next hour hiding.",
}

var driftTemplates = []string{
	"{runner} drifted from {from} to {to}.",
	"{runner} caught a night bus out of {from}. Now running from {to}.",
	"{runner} burned their {from} safehouse and set up in {to}.",
	"{runner} followed a rumor from {from} into {to}.",
}

// collisionTemplates are indexed by collision kind.
var collisionTemplates = [][]string{
	{ // deck crack: winner siphons {gain}
		"{winner} cracked {loser}'s deck and siphoned {gain}.",
		"{winner} found a backdoor in {loser}'s firmware and drained {gain}.",
		"{winner} spoofed {loser}'s handshake and walked out with {gain}.",
	},
	{ // dead-drop race
		"{winner} beat {loser} to a dead drop {place}.",
		"{winner} and {loser} raced for the same dead drop. {winner} got there first.",
		"{loser} arrived at the dead drop {place} to find {winner}'s calling card.",
	},
	{ // hunt
		"{winner} hunted {loser} through the {district}.",
		"{winner} ran {loser} to ground {place}.",
		"{loser} spent an hour running from {winner} through the {district}. It didn't work.",
	},
	{ // drone jam
		"{winner} jammed {loser}'s drone feed and took the shard.",
		"{winner} hijacked {loser}'s drone mid-flight and flew it into a wall.",
		"{loser}'s drone went dark. {winner} was waiting on the other end of the jam.",
	},
}

// cityEventTexts gives each event kind a few ways to be announced.
var cityEventTexts = map[cityEventKind][]string{
	cityEventBlackout: {
		"BLACKOUT rolls across the lower stacks.",
		"BLACKOUT: a substation blew and half the city went dark.",
		"BLACKOUT: the grid browns out, and every deck on backup power stutters.",
	},
	cityEventCorporateSweep: {
		"CORPORATE SWEEP detected. Keep your signatures cold.",
		"CORPORATE SWEEP: security drones are pinging every open port in the city.",
		"CORPORATE SWEEP: a megacorp audit team is walking the Grid with a warrant.",
	},
	cityEventDataLeak: {
		"DATA LEAK: fresh intel is spilling onto the Grid.",
		"DATA LEAK: a disgruntled sysadmin just dumped a corp archive into the open.",
		"DATA LEAK: somebody's backup server is serving files to anyone who asks.",
	},
	cityEventGangWar: {
		"GANG WAR erupts beneath the maglev lines.",
		"GANG WAR: two crews are fighting over the same three blocks.",
		"GANG WAR: the streets are loud tonight and nobody's deck is safe.",
	},
	cityEventBounty: {
		"BOUNTY contract posted; every faction is watching.",
		"BOUNTY: a fixer is paying double for anything off a Meridian server.",
		"BOUNTY: someone put a price on a corp exec's calendar. Everyone wants it.",
	},
	cityEventMegacorpRun: {
		"MEGACORP RUN authorized. The payout is probably a trap.",
		"MEGACORP RUN: a corp is hiring deniable talent again.",
	},
}

var contractCorps = []string{
	"Helix Dynamics", "Kurosawa-Vance", "Obsidian Mutual", "Meridian Biolabs", "Saint Adler Securities", "NovaGrid Utilities",
}

var contractJobs = []string{
	"breach", "extraction", "data heist", "server-farm burn", "prototype snatch", "blackmail run",
}

var levelUpVerbs = []string{"reached", "climbed to", "hit", "clawed up to"}

// render fills {placeholders} from pairs of name, value.
func render(template string, pairs ...string) string {
	replacements := make([]string, 0, len(pairs))
	for i := 0; i+1 < len(pairs); i += 2 {
		replacements = append(replacements, "{"+pairs[i]+"}", pairs[i+1])
	}
	return strings.NewReplacer(replacements...).Replace(template)
}

func (e *Engine) pick(pool []string) string { return pool[e.rng.Intn(len(pool))] }

func (e *Engine) placeIn(district string) string {
	if places := districtPlaces[district]; len(places) > 0 {
		return e.pick(places)
	}
	return "somewhere in " + district
}
