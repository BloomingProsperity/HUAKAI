// Lucide icon helper for the UI kit. Renders a 16px (or sized) stroke icon.
function Icon({ name, size = 16, color, style }) {
  const ref = React.useRef(null);
  React.useEffect(() => {
    if (ref.current && window.lucide) {
      ref.current.innerHTML = "";
      const el = document.createElement("i");
      el.setAttribute("data-lucide", name);
      ref.current.appendChild(el);
      window.lucide.createIcons({
        attrs: { width: size, height: size, stroke: color || "currentColor", "stroke-width": 2 },
      });
    }
  }, [name, size, color]);
  return <span ref={ref} style={{ display: "inline-flex", lineHeight: 0, ...style }} />;
}

window.Icon = Icon;
