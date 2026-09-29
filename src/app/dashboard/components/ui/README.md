# UI kit

Shared building blocks for the dashboard. **Reach for these before writing new markup or CSS.** If you find yourself copying a `className` string between panels, it probably belongs here.

Import from the barrel: `import { Card, SettingRow, Button } from "../components/ui"`.

Styling lives in `src/app/styles/kit.css` (primitives) and `settings.css` (settings shell). Everything reads from the tokens in `styles/base.css` (spacing `--space-*`, type `--text-*`, `--shadow-*`, `--z-*`, `--dur-*`, `--focus-ring`). Do not add raw pixel values or new z-index numbers.

## Which component do I use?

| I want to... | Use |
| --- | --- |
| Group related content on a page | `Card` (title, description, `actions`, `footer`) |
| Title a page or panel | `PageHeader` (h2 by default, because the server header owns the h1) |
| Show a label with a control on the right | `SettingRow`; with an on/off switch, `ToggleRow` |
| Show read-only label/value pairs | `KeyValueList` (`mono` for paths, versions, URLs) |
| Show a notice | `Banner` (`info`, `warning`, `danger`) |
| Hide advanced options | `Disclosure` (native `<details>`) |
| Build a settings screen with sections | `SettingsLayout` + `SettingsSectionPanel`, with `useHashSection` (in `lib/`) for the URL hash |
| Ask the user to confirm | `ConfirmRequest` via the shell's `onConfirm` (see `ConfirmDialog`) |
| Ask for one value | `PromptDialog` (never `window.prompt`) |
| Build any other dialog | `Modal` (focus trap, Escape, aria wiring are built in) |
| Open a menu from a button | `Menu` / `MenuItem` / `MenuSeparator` (never hand-roll outside-click and Escape handling) |
| Explain an icon-only control | `Tooltip`, plus a required `aria-label` on `IconButton` |
| Show a password field | `PasswordInput` |
| Show a field error | `Input error="..."` (sets `aria-invalid` and links the message) |
| Show loading placeholders | `Skeleton` / `SkeletonRows` |
| Copy text | `CopyButton` |
| Show a status | `StatusDot`, `Pill` |

## Buttons

`Button` takes `variant` (`primary`, `danger`, `link`), `size` (`sm`, `md`, `lg`), `block`, `iconLeft`, and `loading` / `loadingText`. Use `size="lg"` for the main call to action in a setup flow. Use `IconButton` for icon-only buttons.

## Unsaved changes and the save bar

Forms that edit saved data register with the shell through `onUnsavedChange`, which takes an `UnsavedChangesRegistration` (`lib/types.ts`). Set `showSaveBar: true` to get the floating `SaveBar`, and provide:

- `onSave` and `onDiscard`, called through refs so they never capture stale state
- `canSave` and `disabledReason`, which the bar shows when Save is blocked
- `saving`, while a save is in flight

Wizards and the file editor register without `showSaveBar`; they keep their own controls but still get the "unsaved changes" navigation guard.

## Page layout

Every page renders inside the same shell: the sidebar, then a header band, then content.

- Server pages use `ServerHeader`. Pages that are not about one server (App settings, Account, Create, Import) use `PageBand` (`components/page-band.tsx`), which shares the server header's layout.
- Inside the content, `PageHeader` titles a panel, and `Tabs` (underline style) switch sections. Settings pages use `SettingsLayout`, which is the same tab style plus icons and unsaved-changes dots.
- Group content in outlined `Card`s (hairline border, transparent fill). Do not add filled panels or a second navigation style.

## Rules of thumb

- One save model per screen: edits collect in local state, the save bar applies them. Instant-apply toggles are fine for safe, reversible actions (for example snapshot settings), and should say so.
- Validate inline next to the field, not only by disabling Save.
- Every interactive element needs a visible keyboard focus state. The global `:focus-visible` rule provides it; do not set `outline: none` without a replacement.
- Keep text at or above `--text-xs` (12px) and use `--text-secondary` or brighter for anything the user must read.
