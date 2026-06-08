import type { Config } from "tailwindcss";

export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        void: "#080B12",
        night: "#0D111C",
        ink: "#111827",
        energy: "#27D3FF",
        jade: "#42F2B3",
        bronze: "#C89B4A",
        risk: "#FF6B4A",
        textMain: "#E8EEF8",
        textMuted: "#8A93A6",
      },
      boxShadow: {
        energy: "0 0 28px rgba(39, 211, 255, 0.28)",
        bronze: "0 0 22px rgba(200, 155, 74, 0.22)",
      },
      fontFamily: {
        sans: [
          "Inter",
          "ui-sans-serif",
          "system-ui",
          "PingFang SC",
          "Microsoft YaHei",
          "sans-serif",
        ],
      },
    },
  },
  plugins: [],
} satisfies Config;
