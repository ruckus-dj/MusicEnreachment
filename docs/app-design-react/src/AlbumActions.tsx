import { useId } from "react";
import type { OriginalRelease } from "./catalogData";
import { Button, Menu, MenuItem, MenuTrigger, Popover } from "./ui";

export function AlbumActions({ release }: { release: OriginalRelease }) {
	const firstTrack = release.tracks[0];
	const menuLabelId = useId();
	return (
		<nav
			className="album-actions"
			aria-label={`Действия альбома ${release.title}`}
		>
			<a className="album-actions-open" href={`#release/${release.id}`}>
				Открыть альбом
			</a>
			{firstTrack && (
				<MenuTrigger>
					<Button className="album-actions-menu-trigger">Теги</Button>
					<Popover className="album-actions-popover" placement="bottom end">
						<span id={menuLabelId} className="sr-only">
							Теги альбома {release.title}
						</span>
						<Menu aria-labelledby={menuLabelId}>
							<MenuItem href={`#tags/${firstTrack.id}/artist`}>
								Теги артиста
							</MenuItem>
							<MenuItem href={`#tags/${firstTrack.id}/release`}>
								Теги альбома
							</MenuItem>
						</Menu>
					</Popover>
				</MenuTrigger>
			)}
		</nav>
	);
}
