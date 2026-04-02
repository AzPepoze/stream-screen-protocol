#pragma once

#include <windows.h>
#include <dxgi1_2.h>
#include <d3d11.h>
#include <wrl/client.h>

using Microsoft::WRL::ComPtr;

// Opaque handle for DXGI duplication
typedef struct
{
     void *device;
     void *deviceContext;
     void *duplication;
     void *stagingTexture;
     int width;
     int height;
} DXGIHandle;

// Frame data returned from capture
typedef struct
{
     unsigned char *data;
     int size;
     int width;
     int height;
} DXGIFrame;

#ifdef __cplusplus
extern "C"
{
#endif

     // Initialize DXGI capture for a specific output (monitor)
     DXGIHandle *DXGI_InitCapture(int outputIndex);

     // Capture next frame
     DXGIFrame *DXGI_CaptureFrame(DXGIHandle *handle);

     // Free frame data
     void DXGI_FreeFrame(DXGIFrame *frame);

     // Release resources
     void DXGI_Release(DXGIHandle *handle);

     // Get error message
     const char *DXGI_GetLastError();

#ifdef __cplusplus
}
#endif
