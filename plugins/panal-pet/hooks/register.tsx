import { atom, read, update } from 'claude-code'
import type { Register } from 'claude-code'

import type { PetFrame, PetMode } from '../types'

// Panal's pet inside Claude Code. It runs `panal pet -stream`, which prints one
// JSON frame per line (the orchestrating agent's mascot as raster cells plus
// a status line), and draws the latest frame above the prompt or in a pane.
// /panal [band|pane|off] switches where it shows; the choice is remembered.

const PANE = 'panal-pet'
const frame = atom({ plugin: 'panal-pet', key: 'frame' } as const, null)
const error = atom({ plugin: 'panal-pet', key: 'error' } as const, null)
const mode = atom({ plugin: 'panal-pet', key: 'mode' } as const, 'band')

const MODES: PetMode[] = ['band', 'pane', 'off']

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const started = await next(e)

    await $.command.register({
      name: 'panal',
      description: 'Panal pet: /panal band, /panal pane or /panal off',
    })
    const saved = (await $.store.get('mode')) as PetMode | undefined
    if (saved && MODES.includes(saved)) {
      await update($, mode, () => saved)
    }
    if ((await read($, mode)) === 'pane') {
      void $.ui.open({ id: PANE, title: 'Panal' })
    }

    // The stream lives as long as the module; if it ends (panal missing, an
    // old panal without `pet`, a crash), try again a little later.
    void (async () => {
      for (let attempt = 0; ; attempt++) {
        let buffer = ''
        try {
          const pet = $.process.spawn({ argv: ['panal', 'pet', '-stream'] })
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
    const want: PetMode = MODES.includes(arg as PetMode)
      ? (arg as PetMode)
      : current === 'off'
        ? 'band'
        : 'off'

    await update($, mode, () => want)
    await $.store.set('mode', want)
    if (want === 'pane') {
      await $.ui.open({ id: PANE, title: 'Panal' })
    } else {
      await $.ui.close({ id: PANE })
    }

    const err = await read($, error)
    const note = err && !(await read($, frame)) ? ` (not running yet: ${err})` : ''
    return { text: `Panal pet: ${want}${note}` }
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    const f = await read($, frame)
    if (!f || (await read($, mode)) !== 'band' || e.props.hasSurvey) {
      return next(e)
    }
    return drawPet($, e, f)
  })

  on('ui.render', { component: 'Pane', requestId: PANE }, async ($, e) => {
    const f = await read($, frame)
    const { Text } = $.ui.resolve(e)
    if (!f) {
      const err = await read($, error)
      return <Text dimColor>{err ? `Waiting for panal pet: ${err}` : 'Waiting for panal pet…'}</Text>
    }
    return drawPet($, e, f)
  })
}

// drawPet draws the mascot with its status lines beside it. Only the terminal
// draws raster cells; other surfaces get the text alone.
function drawPet($: any, e: any, f: PetFrame) {
  if (e.surface === 'terminal') {
    const { Box, Text, Raster } = $.ui.resolve(e)
    return (
      <Box flexDirection="row" gap={1}>
        <Raster key="mascot" columns={f.raster.columns} rows={f.raster.rows} cells={f.raster.cells} />
        <Box flexDirection="column" justifyContent="center">
          <Text color={f.color} bold wrap="truncate">
            {f.line}
          </Text>
          {f.others ? (
            <Text dimColor wrap="truncate">
              {f.others}
            </Text>
          ) : null}
        </Box>
      </Box>
    )
  }
  const { Box, Text } = $.ui.resolve(e)
  return (
    <Box flexDirection="column">
      <Text color={f.color} bold>
        {f.line}
      </Text>
      {f.others ? <Text dimColor>{f.others}</Text> : null}
    </Box>
  )
}
