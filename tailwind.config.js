/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./**/*.templ",
    "./components/**/*.go",
  ],
  plugins: [require("daisyui")],
  daisyui: {
    themes: ["light", "dark", "cupcake", "emerald"],
  },
}
