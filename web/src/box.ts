// Characters of box drawing, blocks, legacy-computing sextants and
// Powerline's separators, drawn rather than taken from the font, as the
// desktop's terminal (MyGo's plugins/terminal/box.go) and Ghostty draw
// them: fonts leave gaps between the lines of cells taller than their
// glyphs, and many lack the separators and the sextants. Everything here
// is in device pixels.

const none = 0, light = 1, heavy = 2, double = 3;

// The arms of U+2500 to U+257F, from their Unicode names: the weights of
// up, right, down and left, a digit each; "" for those drawn otherwise
// (arcs, diagonals) or by the font (dashes).
const boxArms = [
  "0101", "0202", "1010", "2020", "", "", "", "", "", "", "", "", "0110", "0210", "0120", "0220",
  "0011", "0012", "0021", "0022", "1100", "1200", "2100", "2200", "1001", "1002", "2001", "2002", "1110", "1210", "2110", "1120",
  "2120", "2210", "1220", "2220", "1011", "1012", "2011", "1021", "2021", "2012", "1022", "2022", "0111", "0112", "0211", "0212",
  "0121", "0122", "0221", "0222", "1101", "1102", "1201", "1202", "2101", "2102", "2201", "2202", "1111", "1112", "1211", "1212",
  "2111", "1121", "2121", "2112", "2211", "1122", "1221", "2212", "1222", "2122", "2221", "2222", "", "", "", "",
  "0303", "3030", "0310", "0130", "0330", "0013", "0031", "0033", "1300", "3100", "3300", "1003", "3001", "3003", "1310", "3130",
  "3330", "1013", "3031", "3033", "0313", "0131", "0333", "1303", "3101", "3303", "1313", "3131", "3333", "", "", "",
  "", "", "", "", "0001", "1000", "0100", "0010", "0002", "2000", "0200", "0020", "0201", "1020", "0102", "2010",
];

/** Whether the terminal draws cp itself. */
export function drawn(cp: number): boolean {
  if (cp >= 0x2500 && cp <= 0x257f) return boxArms[cp - 0x2500] !== "" || (cp >= 0x256d && cp <= 0x2573);
  return (cp >= 0x2580 && cp <= 0x259f) || (cp >= 0x1fb00 && cp <= 0x1fb3b) || (cp >= 0xe0b0 && cp <= 0xe0b7);
}

/** Whether cp is a shape rather than text, which keeps its colour under the minimum contrast. */
export function noMinContrast(cp: number): boolean {
  return (cp >= 0x2500 && cp <= 0x259f) || (cp >= 0x1fb00 && cp <= 0x1fbff) || (cp >= 0xe0b0 && cp <= 0xe0d7);
}

type Fill = (ax: number, ay: number, bx: number, by: number) => void;

/** Draws cp in the cell at x, y, w by h device pixels, in the context's fill style. */
export function drawBox(ctx: CanvasRenderingContext2D, cp: number, x: number, y: number, w: number, h: number, scale: number) {
  const x0 = Math.round(x), y0 = Math.round(y), x1 = Math.round(x + w), y1 = Math.round(y + h);
  w = x1 - x0;
  h = y1 - y0;
  const fill: Fill = (ax, ay, bx, by) => {
    if (bx > ax && by > ay) ctx.fillRect(ax, ay, bx - ax, by - ay);
  };
  const lw = Math.max(1, Math.round(scale));
  const stroke = (p: Path2D, width: number) => {
    ctx.save();
    ctx.strokeStyle = ctx.fillStyle;
    ctx.lineWidth = width;
    ctx.stroke(p);
    ctx.restore();
  };
  if (cp >= 0x2580 && cp <= 0x259f) return drawBlock(ctx, cp, x0, y0, w, h, fill);
  if (cp >= 0x1fb00 && cp <= 0x1fb3b) return drawSextant(cp, x0, y0, w, h, fill);
  if (cp >= 0xe0b0 && cp <= 0xe0b3) {
    const p = new Path2D();
    if (cp === 0xe0b0 || cp === 0xe0b1) {
      p.moveTo(x0, y0); p.lineTo(x1, y0 + h / 2); p.lineTo(x0, y1);
    } else {
      p.moveTo(x1, y0); p.lineTo(x0, y0 + h / 2); p.lineTo(x1, y1);
    }
    if (cp === 0xe0b0 || cp === 0xe0b2) {
      p.closePath();
      ctx.fill(p);
    } else stroke(p, lw);
    return;
  }
  if (cp >= 0xe0b4 && cp <= 0xe0b7) {
    // Powerline's rounded caps: half an ellipse spanning the cell.
    const k = 0.5523;
    const ym = y0 + h / 2;
    let ex = x1, fx = x0;
    if (cp >= 0xe0b6) [ex, fx] = [x0, x1];
    const kx = fx + (ex - fx) * k, ky = (h / 2) * k;
    const p = new Path2D();
    p.moveTo(fx, y0);
    p.bezierCurveTo(kx, y0, ex, ym - ky, ex, ym);
    p.bezierCurveTo(ex, ym + ky, kx, y1, fx, y1);
    if (cp === 0xe0b4 || cp === 0xe0b6) {
      p.closePath();
      ctx.fill(p);
    } else stroke(p, lw);
    return;
  }
  const cx = x0 + Math.floor((w - lw) / 2);
  const cy = y0 + Math.floor((h - lw) / 2);
  if (cp >= 0x256d && cp <= 0x2573) {
    const p = new Path2D();
    const mx = cx + lw / 2, my = cy + lw / 2;
    const rad = Math.min(w, h) / 2;
    switch (cp) {
      case 0x256d: p.moveTo(mx, y1); p.lineTo(mx, my + rad); p.quadraticCurveTo(mx, my, mx + rad, my); p.lineTo(x1, my); break;
      case 0x256e: p.moveTo(mx, y1); p.lineTo(mx, my + rad); p.quadraticCurveTo(mx, my, mx - rad, my); p.lineTo(x0, my); break;
      case 0x256f: p.moveTo(mx, y0); p.lineTo(mx, my - rad); p.quadraticCurveTo(mx, my, mx - rad, my); p.lineTo(x0, my); break;
      case 0x2570: p.moveTo(mx, y0); p.lineTo(mx, my - rad); p.quadraticCurveTo(mx, my, mx + rad, my); p.lineTo(x1, my); break;
      case 0x2571: p.moveTo(x1, y0); p.lineTo(x0, y1); break;
      case 0x2572: p.moveTo(x0, y0); p.lineTo(x1, y1); break;
      case 0x2573: p.moveTo(x1, y0); p.lineTo(x0, y1); p.moveTo(x0, y0); p.lineTo(x1, y1); break;
    }
    stroke(p, lw);
    return;
  }
  const arms = boxArms[cp - 0x2500];
  if (!arms) return;
  drawArms(fill, [...arms].map(Number), x0, y0, x1, y1, cx + lw / 2, cy + lw / 2, lw);
}

/**
 * Draws the arms of a box drawing character (up, right, down, left), whose
 * lines cross at mx, my, lw pixels wide. A double arm is two light lines,
 * a light line apart, which turn into the arm across them as a corner
 * does, or go through.
 */
function drawArms(fill: Fill, a: number[], x0: number, y0: number, x1: number, y1: number, mx: number, my: number, lw: number) {
  const [up, right, down, left] = a;
  const off = lw;
  const vDouble = up === double || down === double;
  const hDouble = left === double || right === double;
  const thick = (w: number) => (w === heavy ? 2 * lw : w === double ? 2 * off + lw : lw);
  const hw = Math.max(thick(left), thick(right));
  const vw = Math.max(thick(up), thick(down));
  const single = (w: number) => w === light || w === heavy;
  if (single(up)) {
    const t = thick(up);
    const end = hDouble && down === none ? my - off + lw / 2 : my + hw / 2;
    fill(mx - t / 2, y0, mx + t / 2, end);
  }
  if (single(down)) {
    const t = thick(down);
    const start = hDouble && up === none ? my + off - lw / 2 : my - hw / 2;
    fill(mx - t / 2, start, mx + t / 2, y1);
  }
  if (single(left)) {
    const t = thick(left);
    const end = vDouble && right === none ? mx - off + lw / 2 : mx + vw / 2;
    fill(x0, my - t / 2, end, my + t / 2);
  }
  if (single(right)) {
    const t = thick(right);
    const start = vDouble && left === none ? mx + off - lw / 2 : mx - vw / 2;
    fill(start, my - t / 2, x1, my + t / 2);
  }
  for (const side of [-1, 1]) {
    if (vDouble) {
      const lx = mx + side * off - lw / 2;
      const across = side > 0 ? right : left;
      if (up === double && down === double && across !== double) {
        fill(lx, y0, lx + lw, y1);
      } else {
        let inn = my - off + lw / 2, out = my + off + lw / 2;
        if (across === double) out = inn;
        else if (!hDouble) out = my + lw / 2;
        if (up === double) fill(lx, y0, lx + lw, out);
        inn = my + off - lw / 2;
        out = my - off - lw / 2;
        if (across === double) out = inn;
        else if (!hDouble) out = my - lw / 2;
        if (down === double) fill(lx, out, lx + lw, y1);
      }
    }
    if (hDouble) {
      const ly = my + side * off - lw / 2;
      const across = side > 0 ? down : up;
      if (left === double && right === double && across !== double) {
        fill(x0, ly, x1, ly + lw);
      } else {
        let inn = mx - off + lw / 2, out = mx + off + lw / 2;
        if (across === double) out = inn;
        else if (!vDouble) out = mx + lw / 2;
        if (left === double) fill(x0, ly, out, ly + lw);
        inn = mx + off - lw / 2;
        out = mx - off - lw / 2;
        if (across === double) out = inn;
        else if (!vDouble) out = mx - lw / 2;
        if (right === double) fill(out, ly, x1, ly + lw);
      }
    }
  }
}

/** Draws one of U+1FB00 to U+1FB3B: six-bit masks but 0, 21, 42 and 63. */
function drawSextant(cp: number, x0: number, y0: number, w: number, h: number, fill: Fill) {
  const idx = cp - 0x1fb00;
  const pattern = idx + Math.floor(idx / 20) + 1;
  const xs = [x0, x0 + Math.round(w / 2), x0 + w];
  const ys = [y0, y0 + Math.round(h / 3), y0 + Math.round((h * 2) / 3), y0 + h];
  for (let i = 0; i < 6; i++) {
    if (pattern & (1 << i)) fill(xs[i % 2], ys[(i / 2) | 0], xs[(i % 2) + 1], ys[((i / 2) | 0) + 1]);
  }
}

const quadrants: Record<number, string> = {
  0x2596: "0010", 0x2597: "0001", 0x2598: "1000", 0x2599: "1011", 0x259a: "1001",
  0x259b: "1110", 0x259c: "1101", 0x259d: "0100", 0x259e: "0110", 0x259f: "0111",
};

/** Draws a block element (U+2580 to U+259F). */
function drawBlock(ctx: CanvasRenderingContext2D, cp: number, x0: number, y0: number, w: number, h: number, fill: Fill) {
  const rx = (f: number) => x0 + Math.round(w * f);
  const ry = (f: number) => y0 + Math.round(h * f);
  const x1 = x0 + w, y1 = y0 + h;
  if (cp === 0x2580) fill(x0, y0, x1, ry(0.5));
  else if (cp <= 0x2588) fill(x0, ry(1 - (cp - 0x2580) / 8), x1, y1);
  else if (cp <= 0x258f) fill(x0, y0, rx((0x2590 - cp) / 8), y1);
  else if (cp === 0x2590) fill(rx(0.5), y0, x1, y1);
  else if (cp <= 0x2593) {
    // Shades: the colour, a quarter to three quarters opaque.
    const a = ctx.globalAlpha;
    ctx.globalAlpha = a * ((cp - 0x2590) / 4);
    ctx.fillRect(x0, y0, w, h);
    ctx.globalAlpha = a;
  } else if (cp === 0x2594) fill(x0, y0, x1, ry(1 / 8));
  else if (cp === 0x2595) fill(rx(7 / 8), y0, x1, y1);
  else {
    const q = quadrants[cp];
    const mx = rx(0.5), my = ry(0.5);
    if (q[0] === "1") fill(x0, y0, mx, my);
    if (q[1] === "1") fill(mx, y0, x1, my);
    if (q[2] === "1") fill(x0, my, mx, y1);
    if (q[3] === "1") fill(mx, my, x1, y1);
  }
}
