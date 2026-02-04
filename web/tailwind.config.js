/** @type {import('tailwindcss').Config} */
export default {
  content: ["./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      colors: {
        // Jeff's warm design palette
        otter: {
          // Light theme
          bg: "#FAF8F5",
          surface: "#FFFFFF",
          "surface-alt": "#F5F2ED",
          border: "#E8E2D9",
          text: "#2D2A26",
          muted: "#8B7355",
          accent: "#5C4A3D",
          "accent-hover": "#4A3C31",
          orange: "#C87941",
          green: "#5A7A5C",
          red: "#B85C38",
          blue: "#4A6D7C",
          // Dark theme variants
          "dark-bg": "#1A1918",
          "dark-surface": "#252422",
          "dark-surface-alt": "#2D2B28",
          "dark-border": "#3D3A36",
          "dark-text": "#FAF8F5",
          "dark-muted": "#A69582",
          "dark-accent": "#C9A86C",
          "dark-accent-hover": "#D4B87A",
        },
      },
      animation: {
        "fade-in": "fadeIn 0.2s ease-out",
      },
      keyframes: {
        fadeIn: {
          "0%": { opacity: "0", transform: "translateY(10px)" },
          "100%": { opacity: "1", transform: "translateY(0)" },
        },
      },
    },
  },
  plugins: [],
};
