// One frame of `panal pet -stream` (contract v1, see Panal's docs/reference.md).
export type PetAgent = { name: string; status: string; glyph: string; color: string }

export type PetFrame = {
  v: number
  agent: string
  status: string
  glyph: string
  color: string
  mood: string
  line: string
  others: string
  agents: PetAgent[]
  raster: { columns: number; rows: number; cells: string }
}

// Where the pet shows: a band above the prompt, a pane, or nowhere.
export type PetMode = 'band' | 'pane' | 'off'

declare module 'claude-code' {
  interface PluginState {
    'panal-pet': {
      frame: PetFrame | null
      error: string | null
      mode: PetMode
    }
  }
}
