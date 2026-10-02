# UX audit (2026-09-28)

Done with the `tui-design` skill (noise audit and size floor) on the full-screen
preview (`-preview`, then called `-vista`) at 132, 100, 80, 60 and 40 columns,
with real data and with an empty profile.

## What already works

- A single border level (the card); the selected one stands out by a thick border and color, not by another box.
- Status as glyph + word + color (●, ✔, ◐, ⊘, ✖, ○, ⏻): readable without color.
- A semantic palette in `internal/ui` with light/dark variants.
- Responsive: 4 cards → 2×2 → one column; no mascot if it doesn't fit; compact table; paths trimmed in the middle.
- A single animation clock (450 ms) and mascots out of phase.
- Golden screens at 80/100/132/160 columns.

## Findings, by harm to the user

1. ✅ *Done in phase 3.* **First run without guidance** (high, key for other devs). With an empty profile you get 4 "no data · no runs of X yet" cards. It doesn't say what to look for or where: the delegated runs dir, Claude's status line, `~/.codex/sessions`… There should be a welcome screen saying which sources it found and which are missing, and how to connect them. → Phase 3.
2. ✅ *Done in phase 3 (✦ unseen).* **"Done" gets lost** (high). The alert lasts 30 s; if you weren't looking, a "✔ done" card from 1 min ago looks the same as one from 3 days ago. A "done, unseen" state is missing. → Phase 3.
3. ✅ *Done in phase 4 (NO_COLOR, no-animation flag, theme flag).* **Without color, the mascots turn into blobs** (medium). With `NO_COLOR` or an ASCII profile, the half blocks lose the color that makes them a drawing. They should be hidden (or use an outline drawing), and there should be a no-animation option for SSH or slow terminals. → Phase 4.
4. ✅ **Repeated footer** (low). On the dashboard, the footer repeats "1-3 views" and "? help", which are already in the tab row; at 60 columns that splits it into 2 lines. → Fixed in phase 1: "1-3 views" removed from the footer.
5. **Labels on every card** (low). "model", "time" and "task" repeat 4 times and take 8 columns from the task, which is almost always cut off. Kept for now because they help on first use; revisit if the task still doesn't fit.
6. ✅ *Done: if they don't fit with words, they go compact ("● 1  ◐ 1  ○ 2") on the title line.* **Counters and summary sentence say almost the same thing** (low). "■ 1 active ■ 1 out of quota…" and "claude orchestrates · … · opencode out of quota" are on separate lines below 100 columns. They could be merged at those widths.

## Floor

- 132+: 4 cards in one row.
- 80–131: 2×2 with mascot.
- 60: 2×2 with mascot; the task is almost unreadable (finding 5).
- 40: one column, no mascot, 3 lines per card.
- There is no "terminal too small" notice: below ~40 columns it simply clips.
