import { atom, read, update } from 'claude-code'
import type { Register } from 'claude-code'

import type { PetFrame, PetInfo, PetMode } from '../types'

// Panal's pets inside Claude Code. It runs `panal pet -stream -all`, which
// prints one JSON frame per line (every agent's mascot as raster cells plus
// status lines), and draws the latest frame:
//
//   band (default)  a small honeycomb at the right, above the prompt: one
//                   cell per agent, in its status color. Hover it to see the
//                   pets; click "Panal" to keep them open (click again to hide).
//   pane            every pet in a pane.
//   off             nothing.
//
// /panal [band|pane|off] switches; the choice is remembered across sessions.

const PANE = 'panal-pet'
const frame = atom({ plugin: 'panal-pet', key: 'frame' } as const, null)
const error = atom({ plugin: 'panal-pet', key: 'error' } as const, null)
const mode = atom({ plugin: 'panal-pet', key: 'mode' } as const, 'band')
const pinned = atom({ plugin: 'panal-pet', key: 'pinned' } as const, false)

const MODES: PetMode[] = ['band', 'pane', 'off']
// What /panal accepts, near-misses included.
const ALIASES: Record<string, PetMode> = {
  band: 'band', top: 'band', on: 'band', show: 'band',
  pane: 'pane', panel: 'pane', side: 'pane',
  off: 'off', hide: 'off', none: 'off',
}
const CELL = '⬢' // one honeycomb cell per agent

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const started = await next(e)

    await $.command.register({
      name: 'panal',
      description: 'Panal pets: /panal band, /panal pane or /panal off',
    })
    const saved = (await $.store.get('mode')) as PetMode | undefined
    if (saved && MODES.includes(saved)) {
      await update($, mode, () => saved)
    }
    if ((await $.store.get('pinned')) === true) {
      await update($, pinned, () => true)
    }
    if ((await read($, mode)) === 'pane') {
      void $.ui.open({ id: PANE, title: 'Panal' })
    }

    // The stream lives as long as the module; if it ends (panal missing, an
    // old panal without `pet -all`, a crash), try again a little later.
    void (async () => {
      for (let attempt = 0; ; attempt++) {
        let buffer = ''
        try {
          const pet = $.process.spawn({ argv: ['panal', 'pet', '-stream', '-all'] })
          for await (const { stream, text } of pet) {
            if (stream !== 'stdout') continue
            buffer += text
            let nl = buffer.indexOf('\n')
            while (nl >= 0) {
              const line = buffer.slice(0, nl).trim()
              buffer = buffer.slice(nl + 1)
              nl = buffer.indexOf('\n')
              if (!line) continue
              try {
                const f = JSON.parse(line) as PetFrame
                if (f && f.v === 1 && f.raster) {
                  await update($, frame, () => f)
                  await update($, error, () => null)
                  attempt = 0
                }
              } catch {
                // a partial or foreign line: skip it
              }
            }
          }
          await update($, error, () => 'panal pet stopped')
        } catch (err) {
          const msg = String(err)
          await update($, error, () => msg)
          if (attempt === 0) {
            $.ui.toast('panal-pet: could not run "panal pet" (install or update Panal)')
          }
        }
        await $.clock.sleep(Math.min(60_000, 5_000 * (attempt + 1)))
      }
    })()

    return started
  })

  on('command.run', { command: 'panal' }, async ($, e) => {
    const arg = (e.args ?? '').trim().toLowerCase()
    const current = await read($, mode)
    let want: PetMode
    if (arg === '') {
      want = current === 'off' ? 'band' : 'off' // bare /panal toggles
    } else if (ALIASES[arg]) {
      want = ALIASES[arg]
    } else {
      return { text: `Panal pets: unknown "${arg}". Use /panal band, /panal pane or /panal off (now: ${current}).` }
    }

    await update($, mode, () => want)
    await $.store.set('mode', want)
    if (want === 'pane') {
      await $.ui.open({ id: PANE, title: 'Panal' })
    } else {
      await $.ui.close({ id: PANE })
    }

    const err = await read($, error)
    const note = err && !(await read($, frame)) ? ` (not running yet: ${err})` : ''
    return { text: `Panal pets: ${want}${note}` }
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const f = await read($, frame)
    if (!f || (await read($, mode)) !== 'band' || e.props.hasSurvey) {
      return next(e)
    }
    const isPinned = await read($, pinned)
    const { Box, Text, Button } = $.ui.resolve(e)
    const toggle = async () => {
      const now = !(await read($, pinned))
      await update($, pinned, () => now)
      await $.store.set('pinned', now)
    }

    // The whole hive is one hover scope: the pets show while the pointer is
    // over the honeycomb or the pets themselves, and always once pinned.
    return (
      <Box flexDirection="row" justifyContent="flex-end">
        <Box key="hive" flexDirection="column" alignItems="flex-end">
          <Box display={isPinned ? 'flex' : 'none'} hover={{ display: 'flex' }}>
            {drawPets($, e, f)}
          </Box>
          <Box flexDirection="row" gap={1}>
            <Text>
              {agentsOf(f).map(a => (
                <Text color={a.color}>{CELL}</Text>
              ))}
            </Text>
            <Button key="panal" plain dimColor={!isPinned} label="Panal" onPress={toggle} />
          </Box>
        </Box>
      </Box>
    )
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const f = await read($, frame)
    const { Text } = $.ui.resolve(e)
    if (!f) {
      const err = await read($, error)
      return <Text dimColor>{err ? `Waiting for panal pet: ${err}` : 'Waiting for panal pet…'}</Text>
    }
    return drawPane($, e, f)
  })
}

const HONEY = '#F2A900' // Panal's brand color in the pane header

// summaryOf: "1 orchestrating · 2 done", statuses in the order they appear.
function summaryOf(pets: PetInfo[]) {
  const counts = new Map<string, number>()
  for (const p of pets) counts.set(p.status, (counts.get(p.status) ?? 0) + 1)
  return [...counts].map(([status, n]) => `${n} ${status}`).join(' · ')
}

// drawPane: a header, then one card per agent: its mascot on the left; name,
// status, model, task and last action on the right; a rounded border in its
// status color. The pet (the agent orchestrating or working) gets a ★.
function drawPane($: any, e: any, f: PetFrame) {
  const pets = petsOf(f)
  const { Box, Text } = $.ui.resolve(e)
  if (e.surface !== 'terminal') {
    return drawPets($, e, f)
  }
  const { Raster } = $.ui.resolve(e)
  return (
    <Box flexDirection="column" gap={0}>
      <Box flexDirection="row" gap={1} marginBottom={1}>
        <Text color={HONEY} bold>
          ◆ Panal
        </Text>
        <Text dimColor wrap="truncate">
          {summaryOf(pets)}
        </Text>
      </Box>
      {pets.map(p => {
        const status = p.line.startsWith(p.name + ' ') ? p.line.slice(p.name.length + 1) : p.line
        const isPet = p.name === f.agent
        return (
          <Box
            flexDirection="row"
            gap={2}
            borderStyle="round"
            borderColor={p.color}
            borderDimColor={!isPet}
            paddingX={1}
          >
            <Raster key={`card-${p.name}`} columns={p.raster.columns} rows={p.raster.rows} cells={p.raster.cells} />
            <Box flexDirection="column" flexShrink={1}>
              <Text color={p.color} bold wrap="truncate">
                {p.name}
                {isPet ? <Text color={HONEY}> ★</Text> : null}
              </Text>
              <Text color={p.color} wrap="truncate">
                {status}
              </Text>
              {p.model ? (
                <Text dimColor wrap="truncate">
                  {p.model}
                </Text>
              ) : null}
              {p.activity ? (
                <Text italic wrap="truncate">
                  {p.activity}
                </Text>
              ) : p.task ? (
                <Text dimColor wrap="truncate">
                  {p.task}
                </Text>
              ) : null}
            </Box>
          </Box>
        )
      })}
    </Box>
  )
}

// agentsOf: the agents shown, for the honeycomb cells.
function agentsOf(f: PetFrame) {
  return f.agents && f.agents.length > 0 ? f.agents : [{ name: f.agent, status: f.status, glyph: f.glyph, color: f.color }]
}

// petsOf: every agent's own mascot, or just the pet's when panal is older
// than `pet -all`.
function petsOf(f: PetFrame): PetInfo[] {
  if (f.pets && f.pets.length > 0) return f.pets
  return [{ name: f.agent, status: f.status, glyph: f.glyph, color: f.color, mood: f.mood, line: f.line, raster: f.raster }]
}

// drawPets: the mascots side by side, each with its name and status glyph
// under it. Only the terminal draws raster cells; other surfaces get text.
function drawPets($: any, e: any, f: PetFrame) {
  const pets = petsOf(f)
  if (e.surface === 'terminal') {
    const { Box, Text, Raster } = $.ui.resolve(e)
    return (
      <Box flexDirection="row" gap={2}>
        {pets.map(p => (
          <Box flexDirection="column" alignItems="center">
            <Raster key={`pet-${p.name}`} columns={p.raster.columns} rows={p.raster.rows} cells={p.raster.cells} />
            <Text color={p.color} wrap="truncate">
              {p.name} {p.glyph}
            </Text>
          </Box>
        ))}
      </Box>
    )
  }
  const { Box, Text } = $.ui.resolve(e)
  return (
    <Box flexDirection="column">
      {pets.map(p => (
        <Text color={p.color}>{p.line}</Text>
      ))}
    </Box>
  )
}
