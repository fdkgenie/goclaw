// Tailwind v4 ships the PostCSS pipeline in a separate package. Without this
// file Vite inlines tailwindcss/index.css verbatim and the `@tailwind
// utilities;` directive is never expanded, so the SPA ships with zero
// utility classes (the whole dashboard rendered unstyled).
export default {
  plugins: {
    "@tailwindcss/postcss": {},
    autoprefixer: {},
  },
};
