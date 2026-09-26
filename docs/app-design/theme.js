const systemTheme = window.matchMedia('(prefers-color-scheme: dark)');
let themePreference = 'system';
try {
  const savedTheme = localStorage.getItem('music-design-v2-theme');
  if (['system', 'light', 'dark'].includes(savedTheme)) themePreference = savedTheme;
} catch {
  themePreference = 'system';
}
function applyTheme() {
  document.documentElement.dataset.theme = themePreference === 'system' ? (systemTheme.matches ? 'dark' : 'light') : themePreference;
  const control = document.querySelector('#theme-select');
  if (control) control.value = themePreference;
}
applyTheme();
systemTheme.addEventListener('change', applyTheme);
document.addEventListener('DOMContentLoaded', applyTheme);
document.addEventListener('change', event => {
  if (!(event.target instanceof HTMLSelectElement) || event.target.id !== 'theme-select') return;
  themePreference = event.target.value;
  applyTheme();
  try { localStorage.setItem('music-design-v2-theme', themePreference); } catch { document.querySelector('#theme-select').title = 'Тема изменена; браузер не разрешил сохранить выбор.'; }
});
