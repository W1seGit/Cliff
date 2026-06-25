"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Modal } from "./modal";

export interface ImageCropModalProps {
  file: File | null;
  onClose: () => void;
  onCrop: (croppedFile: File) => void;
  title?: string;
  description?: string;
}

const OUTPUT_SIZE = 64;

export function ImageCropModal({
  file,
  onClose,
  onCrop,
  title = "Crop image",
  description = "Drag to position the crop area, then click Apply.",
}: ImageCropModalProps) {
  const [imgEl, setImgEl] = useState<HTMLImageElement | null>(null);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [dragging, setDragging] = useState(false);
  const dragStart = useRef({ x: 0, y: 0, ox: 0, oy: 0 });
  const containerRef = useRef<HTMLDivElement>(null);
  const imgSrc = useMemo(() => file ? URL.createObjectURL(file) : "", [file]);

  useEffect(() => {
    if (!imgSrc) return;
    const img = new Image();
    img.onload = () => {
      setImgEl(img);
      setOffset({ x: 0, y: 0 });
    };
    img.src = imgSrc;
    return () => URL.revokeObjectURL(imgSrc);
  }, [imgSrc]);

  const cropSize = imgEl ? Math.min(imgEl.naturalWidth, imgEl.naturalHeight) : 0;

  const handleMouseDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    setDragging(true);
    dragStart.current = { x: e.clientX, y: e.clientY, ox: offset.x, oy: offset.y };
  }, [offset]);

  useEffect(() => {
    if (!dragging) return;
    const handleMove = (e: MouseEvent) => {
      if (!imgEl || !containerRef.current) return;
      const rect = containerRef.current.getBoundingClientRect();
      const scale = rect.width / imgEl.naturalWidth;
      const dx = (e.clientX - dragStart.current.x) / scale;
      const dy = (e.clientY - dragStart.current.y) / scale;
      const maxX = (imgEl.naturalWidth - cropSize) / 2;
      const maxY = (imgEl.naturalHeight - cropSize) / 2;
      setOffset({
        x: Math.max(-maxX, Math.min(maxX, dragStart.current.ox + dx)),
        y: Math.max(-maxY, Math.min(maxY, dragStart.current.oy + dy)),
      });
    };
    const handleUp = () => setDragging(false);
    window.addEventListener("mousemove", handleMove);
    window.addEventListener("mouseup", handleUp);
    return () => {
      window.removeEventListener("mousemove", handleMove);
      window.removeEventListener("mouseup", handleUp);
    };
  }, [dragging, imgEl, cropSize]);

  function handleApply() {
    if (!imgEl) return;
    const canvas = document.createElement("canvas");
    canvas.width = OUTPUT_SIZE;
    canvas.height = OUTPUT_SIZE;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    ctx.imageSmoothingEnabled = true;
    ctx.imageSmoothingQuality = "high";
    const sx = (imgEl.naturalWidth - cropSize) / 2 + offset.x;
    const sy = (imgEl.naturalHeight - cropSize) / 2 + offset.y;
    ctx.drawImage(imgEl, sx, sy, cropSize, cropSize, 0, 0, OUTPUT_SIZE, OUTPUT_SIZE);
    canvas.toBlob((blob) => {
      if (!blob) return;
      const croppedFile = new File([blob], file?.name ?? "cropped.png", { type: "image/png" });
      onCrop(croppedFile);
    }, "image/png");
  }

  if (!file) return null;

  return (
    <Modal
      isOpen={Boolean(file)}
      onClose={onClose}
      title={title}
      description={description}
      onConfirm={handleApply}
      confirmLabel="Apply"
      form={false}
      rolePresentationClick={false}
    >
      <div className="crop-modal-body">
        {imgSrc && (
          <div
            ref={containerRef}
            className="crop-canvas"
            onMouseDown={handleMouseDown}
            style={{ cursor: dragging ? "grabbing" : "grab" }}
          >
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={imgSrc} alt="Preview" className="crop-image" draggable={false} />
            <div
              className="crop-overlay"
              style={{
                width: cropSize ? `${(cropSize / (imgEl?.naturalWidth ?? 1)) * 100}%` : "100%",
                aspectRatio: "1 / 1",
                transform: `translate(calc(-50% + ${offset.x}px), calc(-50% + ${offset.y}px))`,
              }}
            />
          </div>
        )}
        <p className="crop-hint">The image will be cropped to a square and resized to {OUTPUT_SIZE}&times;{OUTPUT_SIZE}px.</p>
      </div>
    </Modal>
  );
}
