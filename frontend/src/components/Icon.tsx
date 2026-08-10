export function Icon(path: string) {
  return (
    <svg aria-hidden="true" className="icon" viewBox="0 0 24 24">
      <path d={path} />
    </svg>
  );
}
