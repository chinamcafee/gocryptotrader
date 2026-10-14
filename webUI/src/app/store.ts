import { configureStore } from '@reduxjs/toolkit'
import { setupListeners } from '@reduxjs/toolkit/query'
import { gctApi } from './api/gctApi'

export const store = configureStore({
  reducer: {
    [gctApi.reducerPath]: gctApi.reducer,
  },
  middleware: (getDefault) => getDefault({ serializableCheck: false }).concat(gctApi.middleware),
})

setupListeners(store.dispatch)

export type RootState = ReturnType<typeof store.getState>
export type AppDispatch = typeof store.dispatch
