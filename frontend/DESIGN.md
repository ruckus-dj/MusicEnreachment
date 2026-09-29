# Production Setup Manager

The first-run screen follows `docs/app-design/DESIGN.md` and the S01 references in `docs/app-design/screenshots/setup-*.png`: compact system typography, green accent, narrow step indicator, two columns on desktop (form and explanation), and stacked panels on small screens. This is a production UI, not the prototype's demo data or its disabled controls.

Setup and platform state are loaded from the generated API before selecting a route. The server owns completed status, saved settings, and configuration health; the browser stores only the current step and operation IDs for recovery. A platform diagnostic is shown separately from Setup. After completion, health failures remain in the ordinary app rather than reopening Setup.

The six steps are platform, server directories, managed tools, publication format, metadata providers, and server summary. Publication format has no preselected value. Tools catalog selection, exact-path overwrite confirmation, installation progress, retry, and activation are separate actions. Operation SSE is only a wake-up; REST snapshots supply state after connection and reconnection. Completion re-reads server health and invokes server-side validation. All errors stay visible with retry actions; headings and errors receive focus, and controls expose keyboard-accessible labels and text statuses.
