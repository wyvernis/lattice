/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        ink: {
          950: "#0a0e12",
          900: "#0f1419",
          800: "#161d25",
          700: "#1e2833",
          600: "#2a3644",
        },
        signal: {
          cyan: "#3dd6c6",
          amber: "#e8a838",
          coral: "#e85d4c",
          mist: "#8ba3b8",
        },
      },
      fontFamily: {
        display: ["var(--font-display)", "Georgia", "serif"],
        mono: ["var(--font-mono)", "ui-monospace", "monospace"],
        sans: ["var(--font-sans)", "system-ui", "sans-serif"],
      },
      backgroundImage: {
        grid: "linear-gradient(rgba(61,214,198,0.06) 1px, transparent 1px), linear-gradient(90deg, rgba(61,214,198,0.06) 1px, transparent 1px)",
      },
    },
  },
  plugins: [],
};
