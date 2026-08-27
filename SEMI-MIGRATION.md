# Semi Universe Design migration plan

DockPort will use Semi Design's Feishu Universe Design theme as its visual source of truth. The earlier migration replaced interaction primitives, but it still lets project CSS define a separate visual language. This plan supersedes that styling approach.

Official references:

- Semi lists Feishu Universe Design as an official example theme: <https://semi.design/zh-CN/start/design-source>
- Semi's Vite integration applies a DSM theme package through `@douyinfe/semi-vite-plugin`: <https://semi.design/zh-CN/advanced/customize-theme>
- The published Universe theme package is `@semi-bot/semi-theme-universedesign`.

## Non-negotiable boundary

Universe Design owns colors, typography, radii, borders, shadows, density, control sizes, focus treatment, motion, light/dark states, and component interaction states.

DockPort code may own only:

- responsive structure: display, grid/flex, placement, width, height, gap, overflow, and breakpoints;
- Docker-domain information architecture and data visualization configuration;
- Monaco, xterm.js, and ECharts sizing and behavior where Semi has no equivalent component.

DockPort code must not:

- define a project color palette, font scale, radius scale, shadows, gradients, glass effects, decorative grids/orbits/scanlines, or custom theme animations;
- override `.semi-*` selectors to restyle Semi components;
- reproduce Button, Input, Select, navigation, table, card, dialog, feedback, typography, or status appearances with Tailwind or project CSS;
- map Universe tokens into a second DockPort token system when a Semi token or component property can be consumed directly.

Tailwind remains in the required stack, but its use is restricted to layout utilities. Visual utilities such as `bg-*`, `text-*` colors, `border-*` colors, `rounded-*`, `shadow-*`, gradients, and project font styling are outside the allowed boundary.

## Delivered state

The application now compiles `@semi-bot/semi-theme-universedesign` through the official Semi Vite plugin. Semi components and Universe tokens own the visual system; the former project palette, component overrides, cockpit decoration, gradients, shadows, custom radii, and visual Tailwind utilities have been removed. `web/src/styles.css` contains only the Tailwind import, document reset, Universe document colors, and reduced-motion handling.

`npm run audit:universe` rejects Semi CSS selector overrides, project-owned literal CSS colors, and visual Tailwind utilities in JSX. The acceptance suite verified Universe tokens, light/dark/system themes, English/Chinese localization, desktop/mobile navigation, keyboard command access, overlays, resource and delivery forms, Monaco, ECharts, and xterm in Chrome 152.0.7977.64 with no page errors.

## Delivery phases

### 1. Theme compatibility gate

- Add the official `@douyinfe/semi-vite-plugin` and `@semi-bot/semi-theme-universedesign` packages.
- Configure Vite to compile the Universe Design theme package.
- Verify the older published Universe theme (`1.0.13`) against Semi UI 19/Foundation `2.102.x` before broad page work.
- Confirm light/dark token output, component-level variables, production build, and lazy-loaded component CSS.
- If the published package is incompatible, regenerate/clone the Universe theme through Semi DSM rather than copying its CSS into DockPort.

### 2. Application frame

- Rebuild the shell with Semi `Layout`, `Nav`, `Breadcrumb`, `Typography`, `Space`, `Divider`, `Button`, `Dropdown`, and `Select` composition.
- Remove custom shell backgrounds, shadows, selected-nav styling, command-palette styling, and the bespoke theme switch.
- Use the standard Semi theme control and component props; preserve compact/collapsed navigation and `Cmd/Ctrl+K` behavior.

### 3. Resource and operation surfaces

- Standardize lists and metadata on Semi `Table`, `List`, `Descriptions`, `Tag`, `Badge`, `Progress`, `Card`, `Collapse`, `Tabs`, and `Empty` as appropriate.
- Standardize forms on Semi `Form` fields and validation instead of visual wrappers around individual controls.
- Keep dense Docker workflows through Semi size/density props and information architecture, not custom row appearance.
- Preserve confirmations, volume-name verification, secret masking, task progress, audit behavior, and explicit node routing.

### 4. Page-by-page visual removal

- Replace the custom overview cockpit, authentication signal scene, delivery panels, resource frames, and Compose editor chrome with Universe-themed Semi compositions.
- Remove project-owned colors, fonts, radii, borders, shadows, gradients, pseudo-element decoration, and component-state selectors.
- Reduce `styles.css` to Tailwind import, minimal document/layout reset, third-party canvas/terminal sizing, and accessibility rules not supplied by Semi.
- Add a lint/audit rule that rejects `.semi-*` overrides and disallowed visual utility categories.

### 5. Specialized integrations

- Read ECharts series colors from `--semi-color-data-*` and surfaces/text from official Semi variables.
- Select Monaco and xterm built-in light/dark themes from the active Semi mode; do not invent an editor palette.
- Keep only structural CSS required for editor/terminal dimensions and overflow.

### 6. Universe Design acceptance

- Compare representative pages against the Universe theme reference in light and dark modes.
- Exercise Chinese/English locale, system-theme changes, desktop/mobile breakpoints, keyboard navigation, overlays, destructive flows, Monaco, xterm.js, and ECharts.
- Run `npm run lint`, `npm run typecheck`, `npm run build`, `git diff --check`, source-style audits, and isolated browser screenshots.
- Completion requires zero project-owned component appearance overrides and no unexplained visual Tailwind utilities.

## Migration order

Work vertically so every completed slice is reviewable: theme gate → shell/authentication → shared resource frame/forms → Docker resources → Compose → delivery/auth center/settings/tasks → specialized integrations → global style deletion and final visual audit.
