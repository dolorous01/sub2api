export type CanvasColorTheme = "light" | "dark";
export type CanvasBackgroundMode = "dots" | "lines" | "blank";

export const canvasThemes = {
    light: {
        canvas: {
            background: "#eef1f3",
            dot: "rgba(50,62,70,.24)",
            line: "rgba(50,62,70,.11)",
            selectionStroke: "#17745a",
            selectionFill: "rgba(23,116,90,.08)",
        },
        node: {
            label: "#66727b",
            fill: "#f5f7f8",
            panel: "#ffffff",
            stroke: "#d7dde1",
            activeStroke: "#17745a",
            placeholder: "#929ca3",
            text: "#182026",
            muted: "#66727b",
            faint: "#929ca3",
        },
        toolbar: {
            panel: "rgba(255,255,255,.96)",
            border: "#d7dde1",
            item: "#66727b",
            itemHover: "#f0f3f4",
            activeBg: "#e2f3ed",
            activeText: "#105d48",
        },
    },
    dark: {
        canvas: {
            background: "#171a1d",
            dot: "rgba(245,245,244,.24)",
            line: "rgba(245,245,244,.10)",
            selectionStroke: "#48b893",
            selectionFill: "rgba(72,184,147,.12)",
        },
        node: {
            label: "#d6d3d1",
            fill: "#292e32",
            panel: "#202428",
            stroke: "#394147",
            activeStroke: "#48b893",
            placeholder: "#78838b",
            text: "#f0f3f4",
            muted: "#aab3b9",
            faint: "#78838b",
        },
        toolbar: {
            panel: "rgba(32,36,40,.96)",
            border: "#394147",
            item: "#aab3b9",
            itemHover: "#292e32",
            activeBg: "#203d34",
            activeText: "#62c9a7",
        },
    },
} as const;

export type CanvasTheme = (typeof canvasThemes)[CanvasColorTheme];
